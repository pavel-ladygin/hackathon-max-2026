package contracts

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestPrincipalContextRoundTrip(t *testing.T) {
	principal := Principal{UserID: uuid.New()}
	got, ok := PrincipalFromContext(WithPrincipal(context.Background(), principal))
	if !ok || got != principal {
		t.Fatalf("PrincipalFromContext() = %#v, %v; want %#v, true", got, ok, principal)
	}
}

func TestPrincipalFromContextRejectsMissingAndNilPrincipal(t *testing.T) {
	if got, ok := PrincipalFromContext(context.Background()); ok || got != (Principal{}) {
		t.Fatalf("missing principal = %#v, %v; want zero, false", got, ok)
	}
	if got, ok := PrincipalFromContext(WithPrincipal(context.Background(), Principal{})); ok || got != (Principal{}) {
		t.Fatalf("nil principal = %#v, %v; want zero, false", got, ok)
	}
}
