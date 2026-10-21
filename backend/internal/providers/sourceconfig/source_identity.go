package sourceconfig

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SourceKeyWhitespace matches strings.TrimSpace for PostgreSQL btrim checks.
const SourceKeyWhitespace = "\t\n\v\f\r \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000"

var ErrCanonicalIdentityConflict = errors.New("event source canonical identity conflicts with another source")

func canonicalSourceKey(sourceKey string) string {
	return strings.TrimSpace(sourceKey)
}

// ValidateExecutionIdentity rejects legacy generic rows whose trimmed source
// key is ambiguous. It intentionally does not inspect system sources.
func (r *Repository) ValidateExecutionIdentity(ctx context.Context, source Source) error {
	if source.ID == uuid.Nil || !strings.HasPrefix(source.SourceKey, "generic:") {
		return ErrCanonicalIdentityConflict
	}
	var count int
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM event_sources
		WHERE source_kind='generic' AND btrim(source_key,$1)=$2`, SourceKeyWhitespace, canonicalSourceKey(source.SourceKey)).Scan(&count); err != nil {
		return errors.New("event source identity validation failed")
	}
	if count != 1 {
		return ErrCanonicalIdentityConflict
	}
	return nil
}

func lockCanonicalIdentity(ctx context.Context, tx pgx.Tx, sourceKey string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "event-source-canonical:"+canonicalSourceKey(sourceKey)); err != nil {
		return errors.New("event source identity lock unavailable")
	}
	return nil
}

func hasCanonicalIdentity(ctx context.Context, tx pgx.Tx, sourceKey string) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM event_sources
		WHERE source_kind='generic' AND btrim(source_key,$1)=$2)`, SourceKeyWhitespace, canonicalSourceKey(sourceKey)).Scan(&exists); err != nil {
		return false, errors.New("event source identity check failed")
	}
	return exists, nil
}
