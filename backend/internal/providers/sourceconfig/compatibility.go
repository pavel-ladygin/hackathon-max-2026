package sourceconfig

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// CheckSecrets verifies that this process can decrypt every configured source
// credential before serving requests or starting scheduled imports. It never
// returns source identifiers, URLs, ciphertext, or plaintext credentials.
func (r *Repository) CheckSecrets(ctx context.Context) error {
	if r == nil || r.db == nil || r.codec == nil {
		return errors.New("event source secret compatibility check is unavailable")
	}
	rows, err := r.db.Query(ctx, `SELECT id, auth_secret_ciphertext, key_version
		FROM event_sources WHERE auth_secret_ciphertext IS NOT NULL`)
	if err != nil {
		return errors.New("event source secret compatibility check failed")
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var ciphertext []byte
		var version int16
		if err := rows.Scan(&id, &ciphertext, &version); err != nil {
			return errors.New("event source secret compatibility check failed")
		}
		if _, err := r.codec.Open(id, ciphertext, version); err != nil {
			return errors.New("stored event source credentials cannot be decrypted; restore the previous INVITE_ENCRYPTION_KEY and INVITE_ENCRYPTION_KEY_VERSION, or clear and re-enter credentials")
		}
	}
	if rows.Err() != nil {
		return errors.New("event source secret compatibility check failed")
	}
	return nil
}
