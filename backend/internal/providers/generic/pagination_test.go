package generic

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"testing"
	"time"
)

func TestFetchRecordsExtractsResponsePath(t *testing.T) {
	payload := map[string]any{"data": map[string]any{"events": []any{map[string]any{"id": "a"}}}}
	result, err := fetchRecords(context.Background(), HTTPConfig{}, PaginationConfig{ResponsePath: "data.events"}, 1, 10, time.Second, false, func(context.Context, HTTPConfig) (any, error) {
		return payload, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.records) != 1 || !result.complete {
		t.Fatalf("unexpected result: %#v", result)
	}

	root, err := fetchRecords(context.Background(), HTTPConfig{}, PaginationConfig{}, 1, 10, time.Second, false, func(context.Context, HTTPConfig) (any, error) {
		return []any{"root"}, nil
	})
	if err != nil || !reflect.DeepEqual(root.records, []any{"root"}) {
		t.Fatalf("root array result = %#v, %v", root, err)
	}
}

func TestFetchRecordsUsesPageAndPageSizeAndOverridesFixedQuery(t *testing.T) {
	calls := 0
	config := HTTPConfig{Query: url.Values{"page": {"99"}, "page_size": {"1"}, "q": {"fixed"}}}
	pagination := PaginationConfig{Mode: PaginationPage, PageSize: 2}
	result, err := fetchRecords(context.Background(), config, pagination, 5, 10, time.Second, false, func(_ context.Context, got HTTPConfig) (any, error) {
		calls++
		if got.Query.Get("page") != string(rune('0'+calls)) || got.Query.Get("page_size") != "2" || got.Query.Get("q") != "fixed" {
			t.Errorf("page %d query = %v", calls, got.Query)
		}
		if calls == 1 {
			return []any{"a", "b"}, nil
		}
		return []any{"c"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !result.complete || len(result.records) != 3 {
		t.Fatalf("calls=%d result=%#v", calls, result)
	}
}

func TestFetchRecordsUsesOffsetAndLimit(t *testing.T) {
	calls := 0
	pagination := PaginationConfig{Mode: PaginationOffset, Limit: 2}
	_, err := fetchRecords(context.Background(), HTTPConfig{}, pagination, 5, 10, time.Second, false, func(_ context.Context, got HTTPConfig) (any, error) {
		wantOffset := []string{"0", "2"}[calls]
		calls++
		if got.Query.Get("offset") != wantOffset || got.Query.Get("limit") != "2" {
			t.Errorf("query = %v", got.Query)
		}
		if calls == 1 {
			return []any{1, 2}, nil
		}
		return []any{3}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestFetchRecordsHardLimitsAndPreviewTruncation(t *testing.T) {
	pagination := PaginationConfig{Mode: PaginationPage, PageSize: 1}
	fetch := func(context.Context, HTTPConfig) (any, error) { return []any{"more"}, nil }
	result, err := fetchRecords(context.Background(), HTTPConfig{}, pagination, 2, 10, time.Second, false, fetch)
	if !errors.Is(err, ErrIncompleteTraversal) || len(result.records) != 0 {
		t.Fatalf("full traversal = %#v, %v", result, err)
	}
	preview, err := fetchRecords(context.Background(), HTTPConfig{}, pagination, 2, 10, time.Second, true, fetch)
	if err != nil || preview.complete || len(preview.records) != 2 {
		t.Fatalf("preview = %#v, %v", preview, err)
	}

	_, err = fetchRecords(context.Background(), HTTPConfig{}, PaginationConfig{}, 1, 1, time.Second, false, func(context.Context, HTTPConfig) (any, error) {
		return []any{1, 2}, nil
	})
	if !errors.Is(err, ErrIncompleteTraversal) {
		t.Fatalf("event limit error = %v", err)
	}
}

func TestFetchRecordsRejectsBadResponsePathAndPagination(t *testing.T) {
	for _, tc := range []struct {
		name    string
		config  PaginationConfig
		payload any
	}{
		{"missing path", PaginationConfig{ResponsePath: "items"}, map[string]any{}},
		{"non-array path", PaginationConfig{ResponsePath: "items"}, map[string]any{"items": "no"}},
		{"bad page size", PaginationConfig{Mode: PaginationPage}, []any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fetchRecords(context.Background(), HTTPConfig{}, tc.config, 1, 10, time.Second, false, func(context.Context, HTTPConfig) (any, error) { return tc.payload, nil })
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestValidatePaginationRejectsConflictingParameterNames(t *testing.T) {
	tests := []struct {
		name   string
		config PaginationConfig
	}{
		{name: "page names equal", config: PaginationConfig{Mode: PaginationPage, PageParam: "cursor", PageSizeParam: "cursor", PageSize: 10}},
		{name: "page name collides with default size", config: PaginationConfig{Mode: PaginationPage, PageParam: "page_size", PageSize: 10}},
		{name: "offset names equal", config: PaginationConfig{Mode: PaginationOffset, OffsetParam: "cursor", LimitParam: "cursor", Limit: 10}},
		{name: "offset name collides with default limit", config: PaginationConfig{Mode: PaginationOffset, OffsetParam: "limit", Limit: 10}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidatePaginationConfig(test.config); err == nil {
				t.Fatal("expected conflicting pagination parameter names to be rejected")
			}
		})
	}
}

func TestFetchRecordsUsesOverallTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := fetchRecords(ctx, HTTPConfig{}, PaginationConfig{}, 1, 1, time.Nanosecond, false, func(context.Context, HTTPConfig) (any, error) {
		time.Sleep(time.Millisecond)
		return []any{}, nil
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
