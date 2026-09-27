package preferences

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

// Repository stores preferences using the platform PostgreSQL database.
type Repository struct{ pool *store.Pool }

func NewRepository(pool *store.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) get(ctx context.Context, userID uuid.UUID) (Value, bool, error) {
	var value Value
	found := false
	err := r.pool.InTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	}, func(tx pgx.Tx) error {
		q := platform.New(tx)
		row, err := q.GetUserPreferences(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		categories, err := q.ListUserPreferenceCategories(ctx, userID)
		if err != nil {
			return err
		}
		value = valueFromRow(row, categories)
		if err := tx.QueryRow(ctx, `SELECT daily_notifications_enabled FROM users WHERE id = $1`, userID).Scan(&value.DailyNotificationsEnabled); err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil {
		return Value{}, false, err
	}
	return value, found, nil
}

func (r *Repository) replace(ctx context.Context, userID uuid.UUID, input Input) (value Value, err error) {
	err = r.pool.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := platform.New(tx)
		exists, err := q.CityExists(ctx, input.CityID)
		if err != nil {
			return err
		}
		if !exists {
			return ErrInvalid
		}
		if _, err := q.CompleteUserOnboarding(ctx, platform.CompleteUserOnboardingParams{ID: userID, CityID: input.CityID}); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		updated, err := q.UpsertUserPreferences(ctx, platform.UpsertUserPreferencesParams{
			UserID: userID, BudgetMaxMinor: int32(input.BudgetMaxMinor), UsualDayTypes: input.UsualDayTypes, UsualTimeSlots: input.UsualTimeSlots,
		})
		if err != nil {
			return err
		}
		if err := q.DeleteUserPreferenceCategories(ctx, userID); err != nil {
			return err
		}
		for _, slug := range input.InterestSlugs {
			if err := q.AddUserPreferenceCategory(ctx, platform.AddUserPreferenceCategoryParams{UserID: userID, CategorySlug: slug}); err != nil {
				return err
			}
		}
		value = Value{CityID: input.CityID, InterestSlugs: append([]string(nil), input.InterestSlugs...), BudgetMaxMinor: input.BudgetMaxMinor,
			UsualDayTypes: append([]string(nil), input.UsualDayTypes...), UsualTimeSlots: append([]string(nil), input.UsualTimeSlots...),
			Version: int(updated.Version), UpdatedAt: updated.UpdatedAt.Time}
		if err := tx.QueryRow(ctx, `SELECT daily_notifications_enabled FROM users WHERE id = $1`, userID).Scan(&value.DailyNotificationsEnabled); err != nil {
			return err
		}
		return nil
	})
	return value, err
}

func (r *Repository) setDailyNotificationsEnabled(ctx context.Context, userID uuid.UUID, enabled bool) (bool, error) {
	var updated bool
	err := r.pool.QueryRow(ctx, `UPDATE users SET daily_notifications_enabled = $2, updated_at = now() WHERE id = $1 RETURNING daily_notifications_enabled`, userID, enabled).Scan(&updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	return updated, err
}

func valueFromRow(row platform.GetUserPreferencesRow, categories []string) Value {
	return Value{
		CityID: row.CityID, InterestSlugs: categories, BudgetMaxMinor: int(row.BudgetMaxMinor),
		UsualDayTypes: append([]string(nil), row.UsualDayTypes...), UsualTimeSlots: append([]string(nil), row.UsualTimeSlots...),
		Version: int(row.Version), UpdatedAt: row.UpdatedAt.Time,
	}
}
