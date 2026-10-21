// Package eventuploads owns metadata and lifecycle for private event poster uploads.
package eventuploads

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

var (
	ErrUnavailable   = errors.New("event image storage is unavailable")
	ErrNotFound      = errors.New("event image upload not found")
	ErrNotOwner      = errors.New("event image upload belongs to another user")
	ErrClaimed       = errors.New("event image upload is already attached")
	ErrExpired       = errors.New("event image upload expired")
	ErrDeleting      = errors.New("event image upload is being removed")
	ErrQuotaExceeded = errors.New("event image upload quota exceeded")
)

type ObjectStore interface {
	Put(context.Context, string, []byte, string) error
	Get(context.Context, string) ([]byte, string, error)
	Delete(context.Context, string) error
	Available() bool
}

type Upload struct {
	ID           uuid.UUID
	OwnerID      uuid.UUID
	ObjectKey    string
	ContentType  string
	ByteSize     int64
	Width        int
	Height       int
	CreatedAt    time.Time
	SubmissionID *uuid.UUID
}

type Service struct {
	db      *store.Pool
	objects ObjectStore
}

func NewService(db *store.Pool, objects ObjectStore) *Service {
	return &Service{db: db, objects: objects}
}

func (s *Service) Available() bool {
	return s != nil && s.db != nil && s.db.Pool != nil && s.objects != nil && s.objects.Available()
}

func (s *Service) Store(ctx context.Context, ownerID uuid.UUID, data []byte, contentType string, width, height int) (Upload, error) {
	if !s.Available() {
		return Upload{}, ErrUnavailable
	}
	if ownerID == uuid.Nil || len(data) == 0 || len(data) > 10<<20 || width < 1 || height < 1 || width > 40000000/height {
		return Upload{}, errors.New("invalid event image metadata")
	}
	if contentType != "image/jpeg" && contentType != "image/png" && contentType != "image/webp" {
		return Upload{}, errors.New("unsupported event image format")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Upload{}, fmt.Errorf("begin event image upload: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var lockedOwner uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, ownerID).Scan(&lockedOwner); err != nil {
		return Upload{}, fmt.Errorf("lock event image owner: %w", err)
	}
	var pending int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM event_image_uploads WHERE owner_id=$1 AND submission_id IS NULL
		AND created_at >= now() - interval '24 hours'`, ownerID).Scan(&pending); err != nil {
		return Upload{}, fmt.Errorf("check event image upload quota: %w", err)
	}
	if pending >= 20 {
		return Upload{}, ErrQuotaExceeded
	}
	id := uuid.New()
	key := "event-submissions/" + id.String()
	if resolver, ok := s.objects.(interface{ ObjectKey(string) string }); ok {
		key = resolver.ObjectKey(key)
	}
	if key == "" {
		return Upload{}, errors.New("invalid event image object key")
	}
	if err := s.objects.Put(ctx, key, data, contentType); err != nil {
		return Upload{}, fmt.Errorf("store event image object: %w", err)
	}
	cleanupObject := func() { _ = s.objects.Delete(context.WithoutCancel(ctx), key) }
	var result Upload
	err = tx.QueryRow(ctx, `
		INSERT INTO event_image_uploads (id, owner_id, object_key, content_type, byte_size, width, height)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, owner_id, object_key, content_type, byte_size, width, height, created_at, submission_id`,
		id, ownerID, key, contentType, len(data), width, height).Scan(
		&result.ID, &result.OwnerID, &result.ObjectKey, &result.ContentType, &result.ByteSize, &result.Width, &result.Height, &result.CreatedAt, &result.SubmissionID)
	if err != nil {
		cleanupObject()
		return Upload{}, fmt.Errorf("save event image metadata: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Upload{}, fmt.Errorf("commit event image upload metadata: %w", err)
	}
	return result, nil
}

// Claim locks the upload row in the caller's transaction, verifies its owner
// and age, and atomically attaches it to exactly one submission.
func (s *Service) Claim(ctx context.Context, tx pgx.Tx, uploadID, ownerID, submissionID uuid.UUID) (Upload, error) {
	if tx == nil || uploadID == uuid.Nil || ownerID == uuid.Nil || submissionID == uuid.Nil {
		return Upload{}, ErrNotFound
	}
	var result Upload
	var submission *uuid.UUID
	var deleting, expired bool
	err := tx.QueryRow(ctx, `SELECT id, owner_id, object_key, content_type, byte_size, width, height, created_at, submission_id, deleting,
		created_at < now() - interval '24 hours'
		FROM event_image_uploads WHERE id = $1 FOR UPDATE`, uploadID).Scan(
		&result.ID, &result.OwnerID, &result.ObjectKey, &result.ContentType, &result.ByteSize, &result.Width, &result.Height, &result.CreatedAt, &submission, &deleting, &expired)
	if errors.Is(err, pgx.ErrNoRows) {
		return Upload{}, ErrNotFound
	}
	if err != nil {
		return Upload{}, fmt.Errorf("lock event image upload: %w", err)
	}
	if result.OwnerID != ownerID {
		return Upload{}, ErrNotOwner
	}
	if deleting {
		return Upload{}, ErrDeleting
	}
	if expired {
		return Upload{}, ErrExpired
	}
	if submission != nil {
		return Upload{}, ErrClaimed
	}
	if _, err := tx.Exec(ctx, `UPDATE event_image_uploads SET submission_id = $2 WHERE id = $1 AND submission_id IS NULL AND deleting = false`, uploadID, submissionID); err != nil {
		return Upload{}, fmt.Errorf("claim event image upload: %w", err)
	}
	result.SubmissionID = &submissionID
	return result, nil
}

func (s *Service) ByID(ctx context.Context, id uuid.UUID) (Upload, error) {
	if !s.Available() {
		return Upload{}, ErrUnavailable
	}
	return scanUpload(s.db.QueryRow(ctx, `SELECT id, owner_id, object_key, content_type, byte_size, width, height, created_at, submission_id
		FROM event_image_uploads WHERE id = $1 AND deleting = false`, id))
}

// ByIDTx loads metadata through the caller's transaction, avoiding a second
// pool checkout when callers already hold the only connection.
func (s *Service) ByIDTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Upload, error) {
	if tx == nil {
		return Upload{}, ErrNotFound
	}
	return scanUpload(tx.QueryRow(ctx, `SELECT id, owner_id, object_key, content_type, byte_size, width, height, created_at, submission_id
		FROM event_image_uploads WHERE id = $1 AND deleting = false`, id))
}

func scanUpload(row pgx.Row) (Upload, error) {
	var upload Upload
	err := row.Scan(
		&upload.ID, &upload.OwnerID, &upload.ObjectKey, &upload.ContentType, &upload.ByteSize, &upload.Width, &upload.Height, &upload.CreatedAt, &upload.SubmissionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Upload{}, ErrNotFound
	}
	if err != nil {
		return Upload{}, fmt.Errorf("load event image upload: %w", err)
	}
	return upload, nil
}

func (s *Service) Fetch(ctx context.Context, id uuid.UUID) ([]byte, string, error) {
	upload, err := s.ByID(ctx, id)
	if err != nil {
		return nil, "", err
	}
	data, contentType, err := s.objects.Get(ctx, upload.ObjectKey)
	if err != nil {
		return nil, "", fmt.Errorf("read event image object: %w", err)
	}
	if contentType != upload.ContentType {
		return nil, "", errors.New("stored event image content type does not match metadata")
	}
	return data, contentType, nil
}

// CleanupUnattached expires stale rows through a deletion lease. Claim and
// cleanup serialize on the same row; interrupted leases are retried after five
// minutes, while claimed uploads are never selected.
func (s *Service) CleanupUnattached(ctx context.Context, limit int) (int, error) {
	if !s.Available() {
		return 0, ErrUnavailable
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		WITH candidates AS (
			SELECT id FROM event_image_uploads
			WHERE submission_id IS NULL AND created_at < now() - interval '24 hours'
			  AND (NOT deleting OR deleting_at < now() - interval '5 minutes')
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE event_image_uploads u SET deleting = true, deleting_at = now()
		FROM candidates c WHERE u.id = c.id
		RETURNING u.id, u.object_key`, limit)
	if err != nil {
		return 0, fmt.Errorf("lease stale event image uploads: %w", err)
	}
	type candidate struct {
		id  uuid.UUID
		key string
	}
	items := make([]candidate, 0, limit)
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.key); err != nil {
			rows.Close()
			return 0, fmt.Errorf("read stale event image upload: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate stale event image uploads: %w", err)
	}
	rows.Close()
	removed := 0
	for _, item := range items {
		if err := s.objects.Delete(ctx, item.key); err != nil {
			_, resetErr := s.db.Exec(context.WithoutCancel(ctx), `UPDATE event_image_uploads SET deleting = false, deleting_at = NULL WHERE id = $1 AND submission_id IS NULL AND deleting`, item.id)
			if resetErr != nil {
				return removed, fmt.Errorf("delete stale event image object: %w; reset lease: %v", err, resetErr)
			}
			return removed, fmt.Errorf("delete stale event image object: %w", err)
		}
		if _, err := s.db.Exec(ctx, `DELETE FROM event_image_uploads WHERE id = $1 AND submission_id IS NULL AND deleting`, item.id); err != nil {
			return removed, fmt.Errorf("remove stale event image metadata after object deletion: %w", err)
		}
		removed++
	}
	return removed, nil
}
