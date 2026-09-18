package contracts

import (
	"context"

	"github.com/google/uuid"
)

// Principal carries only internal identity across module boundaries.
type Principal struct {
	UserID uuid.UUID
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	return principal, ok && principal.UserID != uuid.Nil
}
