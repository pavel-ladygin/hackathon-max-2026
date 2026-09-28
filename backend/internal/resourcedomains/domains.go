// Package resourcedomains manages source-scoped approvals for external images
// and ticket destinations.
package resourcedomains

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

var ErrInvalidHostname = errors.New("hostname is invalid")

type Purpose string

const (
	PurposeImage  Purpose = "image"
	PurposeTicket Purpose = "ticket"
)

type Domain struct {
	ID        uuid.UUID `json:"id"`
	SourceID  uuid.UUID `json:"source_id"`
	Hostname  string    `json:"hostname"`
	Purpose   Purpose   `json:"purpose"`
	CreatedAt time.Time `json:"created_at"`
}

type Repository struct{ db *store.Pool }

func NewRepository(db *store.Pool) (*Repository, error) {
	if db == nil {
		return nil, errors.New("resource domain database is required")
	}
	return &Repository{db: db}, nil
}

func ValidPurpose(purpose Purpose) bool { return purpose == PurposeImage || purpose == PurposeTicket }

// NormalizeHostname accepts one DNS hostname only, canonicalized for exact
// case-insensitive matching. Wildcards, URL components, IPs and local names
// are rejected.
func NormalizeHostname(hostname string) (string, error) {
	hostname = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(hostname)), ".")
	if hostname == "" || strings.ContainsAny(hostname, ":/@?#\\*%[]") || net.ParseIP(hostname) != nil || len(hostname) > 253 {
		return "", ErrInvalidHostname
	}
	labels := strings.Split(hostname, ".")
	if len(labels) < 2 || strings.Trim(labels[len(labels)-1], "0123456789") == "" {
		return "", ErrInvalidHostname
	}
	last := labels[len(labels)-1]
	if strings.HasPrefix(last, "0x") && strings.Trim(last[2:], "0123456789abcdef") == "" {
		return "", ErrInvalidHostname
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", ErrInvalidHostname
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
				return "", ErrInvalidHostname
			}
		}
	}
	if hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") || strings.HasSuffix(hostname, ".local") || strings.HasSuffix(hostname, ".internal") || strings.HasSuffix(hostname, ".home.arpa") || hostname == "home.arpa" || strings.HasSuffix(hostname, ".lan") || strings.HasSuffix(hostname, ".intranet") || strings.HasSuffix(hostname, ".private") || strings.HasSuffix(hostname, ".home") || strings.HasSuffix(hostname, ".corp") || strings.HasSuffix(hostname, ".onion") {
		return "", ErrInvalidHostname
	}
	return hostname, nil
}

// ResourceHostname extracts a normalized hostname from an HTTPS resource URL.
// URL paths and queries are intentionally discarded by callers.
func ResourceHostname(raw string) (string, error) {
	if raw != strings.TrimSpace(raw) || strings.ContainsAny(raw, "\r\n\t\\#") {
		return "", ErrInvalidHostname
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" || strings.HasSuffix(parsed.Host, ":") {
		return "", ErrInvalidHostname
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return "", ErrInvalidHostname
	}
	return NormalizeHostname(parsed.Hostname())
}

func (r *Repository) List(ctx context.Context, sourceID uuid.UUID) ([]Domain, error) {
	rows, err := r.db.Query(ctx, `SELECT id,source_id,hostname,purpose,created_at FROM event_source_allowed_domains WHERE source_id=$1 AND enabled ORDER BY purpose,hostname,id`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	domains := make([]Domain, 0)
	for rows.Next() {
		var domain Domain
		if err := rows.Scan(&domain.ID, &domain.SourceID, &domain.Hostname, &domain.Purpose, &domain.CreatedAt); err != nil {
			return nil, err
		}
		domains = append(domains, domain)
	}
	return domains, rows.Err()
}

// Add is idempotent for the source/hostname/purpose tuple and re-enables an
// explicitly re-approved domain after a prior revoke.
func (r *Repository) Add(ctx context.Context, sourceID uuid.UUID, hostname string, purpose Purpose) (Domain, error) {
	hostname, err := NormalizeHostname(hostname)
	if err != nil {
		return Domain{}, err
	}
	if !ValidPurpose(purpose) {
		return Domain{}, errors.New("resource domain purpose is invalid")
	}
	var domain Domain
	err = r.db.QueryRow(ctx, `INSERT INTO event_source_allowed_domains(id,source_id,hostname,purpose,enabled) VALUES($1,$2,$3,$4,true) ON CONFLICT(source_id,hostname,purpose) DO UPDATE SET enabled=true RETURNING id,source_id,hostname,purpose,created_at`, uuid.New(), sourceID, hostname, purpose).Scan(&domain.ID, &domain.SourceID, &domain.Hostname, &domain.Purpose, &domain.CreatedAt)
	return domain, err
}

func (r *Repository) Revoke(ctx context.Context, sourceID, domainID uuid.UUID) (bool, error) {
	tag, err := r.db.Exec(ctx, `UPDATE event_source_allowed_domains SET enabled=false WHERE source_id=$1 AND id=$2 AND enabled`, sourceID, domainID)
	return tag.RowsAffected() > 0, err
}

func (r *Repository) Allowed(ctx context.Context, sourceID uuid.UUID, hostname string, purpose Purpose) (bool, error) {
	hostname, err := NormalizeHostname(hostname)
	if err != nil || !ValidPurpose(purpose) {
		return false, nil
	}
	var allowed bool
	err = r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM event_source_allowed_domains WHERE source_id=$1 AND hostname=$2 AND purpose=$3 AND enabled)`, sourceID, hostname, purpose).Scan(&allowed)
	return allowed, err
}

func (r *Repository) AllowedBySourceKey(ctx context.Context, sourceKey, hostname string, purpose Purpose) (bool, error) {
	hostname, err := NormalizeHostname(hostname)
	if err != nil || !ValidPurpose(purpose) {
		return false, nil
	}
	var allowed bool
	err = r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM event_sources s JOIN event_source_allowed_domains d ON d.source_id=s.id WHERE s.source_key=$1 AND d.hostname=$2 AND d.purpose=$3 AND d.enabled)`, sourceKey, hostname, purpose).Scan(&allowed)
	return allowed, err
}
