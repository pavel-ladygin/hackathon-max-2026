package migrations

import (
	"slices"
	"testing"
)

func TestExpectedVersionsIncludesFoundation(t *testing.T) {
	versions, err := ExpectedVersions()
	if err != nil {
		t.Fatal(err)
	}
	// The frozen foundation versions must remain present when later features
	// append migrations. In particular, a broken filename parser must not make
	// an unmigrated database appear current.
	want := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	if len(versions) < len(want) || !slices.Equal(versions[:len(want)], want) {
		t.Fatalf("foundation versions = %v, want prefix %v", versions, want)
	}
	if versions[len(versions)-1] != 22 {
		t.Fatalf("latest migration version = %d, want 22", versions[len(versions)-1])
	}
}
