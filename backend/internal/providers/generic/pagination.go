package generic

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// MaxPaginationPages caps the number of upstream requests in one full sync.
	MaxPaginationPages = 100
	// MaxPaginationEvents caps the number of records processed in one full sync.
	MaxPaginationEvents = 10_000
	// MaxPaginationPageSize prevents a source configuration from requesting huge pages.
	MaxPaginationPageSize = 500
	// PreviewMaxPages bounds the interactive preview to a small number of requests.
	PreviewMaxPages = 2
	// FullSyncTimeout bounds the entire multi-page traversal, including all requests.
	FullSyncTimeout = 2 * time.Minute
)

// ErrIncompleteTraversal means a hard traversal limit was reached while the
// source could still have more records.
var ErrIncompleteTraversal = errors.New("generic source traversal is incomplete: hard limit reached")

type PaginationMode string

const (
	PaginationNone   PaginationMode = "none"
	PaginationPage   PaginationMode = "page"
	PaginationOffset PaginationMode = "offset"
)

// PaginationConfig describes how to select the records array and request
// successive pages. An empty ResponsePath selects the JSON root.
type PaginationConfig struct {
	ResponsePath  string         `json:"response_path,omitempty"`
	Mode          PaginationMode `json:"mode"`
	PageParam     string         `json:"page_param,omitempty"`
	PageSizeParam string         `json:"page_size_param,omitempty"`
	OffsetParam   string         `json:"offset_param,omitempty"`
	LimitParam    string         `json:"limit_param,omitempty"`
	PageSize      int            `json:"page_size,omitempty"`
	Limit         int            `json:"limit,omitempty"`
}

// FetchRecords retrieves all records up to the fixed sync limits. If a limit
// may have truncated the source, it returns ErrIncompleteTraversal.
func FetchRecords(ctx context.Context, httpConfig HTTPConfig, pagination PaginationConfig) ([]any, error) {
	records, _, err := FetchRecordsWithStats(ctx, httpConfig, pagination)
	return records, err
}

// FetchRecordsWithStats also reports the number of successfully fetched pages
// for provider_sync_runs accounting.
func FetchRecordsWithStats(ctx context.Context, httpConfig HTTPConfig, pagination PaginationConfig) ([]any, int, error) {
	pages := 0
	fetch := func(ctx context.Context, config HTTPConfig) (any, error) {
		value, err := FetchJSON(ctx, config)
		if err == nil {
			pages++
		}
		return value, err
	}
	result, err := fetchRecords(ctx, httpConfig, pagination, MaxPaginationPages, MaxPaginationEvents, FullSyncTimeout, false, fetch)
	return result.records, pages, err
}

// PreviewRecords retrieves at most two pages. complete is false when the
// preview stopped at that bound while additional records may exist.
func PreviewRecords(ctx context.Context, httpConfig HTTPConfig, pagination PaginationConfig) (records []any, complete bool, err error) {
	records, complete, _, err = PreviewRecordsWithStatus(ctx, httpConfig, pagination)
	return records, complete, err
}

// PreviewRecordsWithStatus reports the last fetched upstream response status.
func PreviewRecordsWithStatus(ctx context.Context, httpConfig HTTPConfig, pagination PaginationConfig) ([]any, bool, int, error) {
	status := 0
	fetch := func(ctx context.Context, config HTTPConfig) (any, error) {
		value, code, err := FetchJSONWithStatus(ctx, config)
		status = code
		return value, err
	}
	result, err := fetchRecords(ctx, httpConfig, pagination, PreviewMaxPages, MaxPaginationEvents, FullSyncTimeout, true, fetch)
	return result.records, result.complete, status, err
}

type traversalResult struct {
	records  []any
	complete bool
}

type jsonFetcher func(context.Context, HTTPConfig) (any, error)

func fetchRecords(ctx context.Context, httpConfig HTTPConfig, pagination PaginationConfig, maxPages, maxEvents int, timeout time.Duration, preview bool, fetch jsonFetcher) (traversalResult, error) {
	if fetch == nil {
		return traversalResult{}, errors.New("JSON fetcher is unavailable")
	}
	if err := validatePagination(pagination); err != nil {
		return traversalResult{}, err
	}
	if maxPages <= 0 || maxEvents <= 0 || timeout <= 0 {
		return traversalResult{}, errors.New("traversal limits are invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	baseQuery := cloneValues(httpConfig.Query)
	var records []any
	for page := 0; page < maxPages; page++ {
		if err := ctx.Err(); err != nil {
			if preview {
				return traversalResult{records: records}, errors.New("generic source traversal timed out")
			}
			return traversalResult{}, errors.New("generic source traversal timed out")
		}
		requestConfig := httpConfig
		requestConfig.Query = cloneValues(baseQuery)
		requestPaginationParams(&requestConfig.Query, pagination, page)
		payload, err := fetch(ctx, requestConfig)
		if err != nil {
			return traversalResult{}, err
		}
		if err := ctx.Err(); err != nil {
			if preview {
				return traversalResult{records: records}, errors.New("generic source traversal timed out")
			}
			return traversalResult{}, errors.New("generic source traversal timed out")
		}
		pageRecords, err := recordsFromPayload(payload, pagination.ResponsePath)
		if err != nil {
			return traversalResult{}, err
		}
		if len(pageRecords) > maxEvents-len(records) {
			if preview {
				remaining := maxEvents - len(records)
				if remaining > 0 {
					records = append(records, pageRecords[:remaining]...)
				}
				return traversalResult{records: records, complete: false}, nil
			}
			return traversalResult{}, ErrIncompleteTraversal
		}
		records = append(records, pageRecords...)
		if pagination.Mode == PaginationNone || !hasMorePossible(pagination, len(pageRecords)) {
			return traversalResult{records: records, complete: true}, nil
		}
		if page+1 == maxPages {
			if preview {
				return traversalResult{records: records, complete: false}, nil
			}
			return traversalResult{}, ErrIncompleteTraversal
		}
		if len(records) == maxEvents {
			if preview {
				return traversalResult{records: records, complete: false}, nil
			}
			return traversalResult{}, ErrIncompleteTraversal
		}
	}
	return traversalResult{records: records, complete: true}, nil
}

func validatePagination(p PaginationConfig) error {
	if strings.TrimSpace(p.ResponsePath) != "" {
		for _, segment := range strings.Split(p.ResponsePath, ".") {
			if strings.TrimSpace(segment) == "" {
				return errors.New("response_path is invalid")
			}
		}
	}
	switch p.Mode {
	case "", PaginationNone:
		return nil
	case PaginationPage:
		if !validQueryName(p.PageParam, "page") || !validQueryName(p.PageSizeParam, "page_size") || p.PageSize < 1 || p.PageSize > MaxPaginationPageSize {
			return errors.New("page pagination configuration is invalid")
		}
		if effectiveQueryName(p.PageParam, "page") == effectiveQueryName(p.PageSizeParam, "page_size") {
			return errors.New("page and page size parameters must be different")
		}
	case PaginationOffset:
		if !validQueryName(p.OffsetParam, "offset") || !validQueryName(p.LimitParam, "limit") || p.Limit < 1 || p.Limit > MaxPaginationPageSize {
			return errors.New("offset pagination configuration is invalid")
		}
		if effectiveQueryName(p.OffsetParam, "offset") == effectiveQueryName(p.LimitParam, "limit") {
			return errors.New("offset and limit parameters must be different")
		}
	default:
		return errors.New("pagination mode is invalid")
	}
	return nil
}

// ValidatePaginationConfig checks pagination and response-path syntax without
// fetching the source.
func ValidatePaginationConfig(p PaginationConfig) error { return validatePagination(p) }

func validQueryName(value, fallback string) bool {
	if value == "" {
		value = fallback
	}
	return strings.TrimSpace(value) == value && value != ""
}

func effectiveQueryName(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func requestPaginationParams(query *url.Values, pagination PaginationConfig, page int) {
	switch pagination.Mode {
	case PaginationPage:
		pageName := pagination.PageParam
		if pageName == "" {
			pageName = "page"
		}
		sizeName := pagination.PageSizeParam
		if sizeName == "" {
			sizeName = "page_size"
		}
		query.Set(pageName, fmt.Sprint(page+1))
		query.Set(sizeName, fmt.Sprint(pagination.PageSize))
	case PaginationOffset:
		offsetName := pagination.OffsetParam
		if offsetName == "" {
			offsetName = "offset"
		}
		limitName := pagination.LimitParam
		if limitName == "" {
			limitName = "limit"
		}
		query.Set(offsetName, fmt.Sprint(page*pagination.Limit))
		query.Set(limitName, fmt.Sprint(pagination.Limit))
	}
}

func hasMorePossible(p PaginationConfig, count int) bool {
	switch p.Mode {
	case PaginationPage:
		return count >= p.PageSize
	case PaginationOffset:
		return count >= p.Limit
	default:
		return false
	}
}

func recordsFromPayload(payload any, path string) ([]any, error) {
	value := payload
	if strings.TrimSpace(path) != "" {
		var found bool
		var err error
		value, found, err = resolvePath(payload, path)
		if err != nil || !found {
			return nil, errors.New("response_path could not be resolved")
		}
	}
	records, ok := value.([]any)
	if !ok {
		return nil, errors.New("response_path must select an array")
	}
	return records, nil
}

func cloneValues(source url.Values) url.Values {
	clone := make(url.Values, len(source))
	for key, values := range source {
		clone[key] = append([]string(nil), values...)
	}
	return clone
}
