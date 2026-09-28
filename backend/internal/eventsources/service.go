// Package eventsources exposes the administrative workflow for configurable event sources.
package eventsources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/catalogseed"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/generic"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers/sourceconfig"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/resourcedomains"
)

const previewSize = 5

type configuration struct {
	http       generic.HTTPConfig
	pagination generic.PaginationConfig
	mapping    generic.Config
}

type mappingFields struct {
	generic.Fields
	PriceFrom generic.FieldMapping `json:"price_from"`
	PriceTo   generic.FieldMapping `json:"price_to"`
}

func configured(source sourceconfig.Source, secret string) (configuration, error) {
	query := make(url.Values, len(source.QueryParams))
	for key, value := range source.QueryParams {
		query.Set(key, value)
	}
	result := configuration{http: generic.HTTPConfig{
		Endpoint: source.EndpointURL, Query: query,
		Auth: generic.AuthConfig{Mode: generic.AuthMode(source.AuthType), Key: secret, Token: secret},
	}}
	if source.AuthType == string(generic.AuthAPIKeyHeader) {
		result.http.Auth.HeaderName = source.AuthName
	}
	if source.AuthType == string(generic.AuthAPIKeyQuery) {
		result.http.Auth.QueryName = source.AuthName
	}
	if err := generic.ValidateHTTPConfig(result.http); err != nil {
		return configuration{}, err
	}
	if len(source.Pagination) > 0 {
		if err := json.Unmarshal(source.Pagination, &result.pagination); err != nil {
			return configuration{}, errors.New("pagination configuration is invalid")
		}
	}
	result.pagination.ResponsePath = source.ResponsePath
	if err := generic.ValidatePaginationConfig(result.pagination); err != nil {
		return configuration{}, err
	}
	if source.AuthType == string(generic.AuthAPIKeyQuery) && paginationUsesParameter(result.pagination, source.AuthName) {
		return configuration{}, errors.New("API key query parameter conflicts with pagination")
	}
	var fields mappingFields
	decoder := json.NewDecoder(bytes.NewReader(source.Mapping))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fields); err != nil {
		return configuration{}, errors.New("mapping configuration is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return configuration{}, errors.New("mapping configuration is invalid")
	}
	result.mapping = generic.Config{
		SourceKey: source.SourceKey, Fields: fields.Fields,
		PriceFrom: fields.PriceFrom, PriceTo: fields.PriceTo,
		PriceUnit: generic.PriceUnit(source.PriceUnit),
		Defaults: generic.Defaults{
			Category: source.Defaults.Category, Timezone: source.Defaults.Timezone,
			Currency: source.Defaults.Currency, Status: source.Defaults.Status,
		},
	}
	if err := generic.ValidateMappings(result.mapping.Fields, result.mapping.PriceFrom, result.mapping.PriceTo); err != nil {
		return configuration{}, err
	}
	for name, field := range map[string]generic.FieldMapping{
		"external_id": result.mapping.Fields.ExternalID,
		"title":       result.mapping.Fields.Title,
		"starts_at":   result.mapping.Fields.StartsAt,
		"venue_name":  result.mapping.Fields.VenueName,
	} {
		if strings.TrimSpace(field.Path) == "" && (field.Default == nil || strings.TrimSpace(*field.Default) == "") {
			return configuration{}, fmt.Errorf("%s mapping is required", name)
		}
	}
	if result.mapping.Defaults.Category == "" || result.mapping.Defaults.Timezone == "" || result.mapping.Defaults.Currency == "" || result.mapping.Defaults.Status == "" {
		return configuration{}, errors.New("source defaults are required")
	}
	if !catalogseed.IsCategorySlug(strings.TrimSpace(result.mapping.Defaults.Category)) {
		return configuration{}, errors.New("default category is invalid")
	}
	if _, err := time.LoadLocation(strings.TrimSpace(result.mapping.Defaults.Timezone)); err != nil {
		return configuration{}, errors.New("default timezone is invalid")
	}
	status := strings.TrimSpace(result.mapping.Defaults.Status)
	if status != providers.EventStatusPublished && status != providers.EventStatusSoldOut && status != providers.EventStatusCancelled {
		return configuration{}, errors.New("default status is invalid")
	}
	if result.mapping.PriceUnit != generic.PriceUnitMajor && result.mapping.PriceUnit != generic.PriceUnitMinor {
		return configuration{}, errors.New("price unit must be major or minor")
	}
	return result, nil
}

func paginationUsesParameter(p generic.PaginationConfig, name string) bool {
	switch p.Mode {
	case generic.PaginationPage:
		pageName, sizeName := p.PageParam, p.PageSizeParam
		if pageName == "" {
			pageName = "page"
		}
		if sizeName == "" {
			sizeName = "page_size"
		}
		return name == pageName || name == sizeName
	case generic.PaginationOffset:
		offsetName, limitName := p.OffsetParam, p.LimitParam
		if offsetName == "" {
			offsetName = "offset"
		}
		if limitName == "" {
			limitName = "limit"
		}
		return name == offsetName || name == limitName
	default:
		return false
	}
}

// ValidateInput checks the parts of a source configuration that do not require
// an upstream request. An unchanged stored secret is represented by a placeholder.
func ValidateInput(input sourceconfig.Input) error {
	secret := input.Secret
	if secret == "" && input.AuthType != string(generic.AuthNone) {
		secret = "configured-secret"
	}
	return validateSource(input, secret)
}

func validateSource(input sourceconfig.Input, secret string) error {
	if !strings.HasPrefix(input.SourceKey, "generic:") || strings.TrimSpace(strings.TrimPrefix(input.SourceKey, "generic:")) == "" || strings.TrimSpace(input.Name) == "" {
		return errors.New("source_key and name are required")
	}
	if len(input.TransformConfig) > 0 {
		var transformConfig map[string]json.RawMessage
		if err := json.Unmarshal(input.TransformConfig, &transformConfig); err != nil || transformConfig == nil || len(transformConfig) != 0 {
			return errors.New("transform_config is unsupported; use field mapping transforms")
		}
	}
	returnSource := sourceconfig.Source{
		SourceKey: input.SourceKey, EndpointURL: input.EndpointURL,
		AuthType: input.AuthType, AuthName: input.AuthName,
		QueryParams: input.QueryParams, ResponsePath: input.ResponsePath,
		Pagination: input.Pagination, Mapping: input.Mapping,
		Defaults: input.Defaults, PriceUnit: input.PriceUnit,
	}
	_, err := configured(returnSource, secret)
	return err
}

type PreviewError struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

type PreviewEvent struct {
	Title          string    `json:"title"`
	StartsAt       time.Time `json:"starts_at"`
	Venue          string    `json:"venue"`
	PriceFromMinor *int32    `json:"price_from_minor,omitempty"`
}

type PreviewResult struct {
	ConnectionOK    bool             `json:"connection_ok"`
	HTTPStatus      int              `json:"http_status"`
	Received        int              `json:"received"`
	Valid           int              `json:"valid"`
	Invalid         int              `json:"invalid"`
	Errors          []PreviewError   `json:"errors"`
	Warnings        []PreviewError   `json:"warnings"`
	Preview         []PreviewEvent   `json:"preview"`
	ResourceDomains []ResourceDomain `json:"resource_domains"`
	Complete        bool             `json:"complete"`
}

type ResourceDomain struct {
	Hostname string                  `json:"hostname"`
	Purpose  resourcedomains.Purpose `json:"purpose"`
	Approved bool                    `json:"approved"`
	Count    int                     `json:"count"`
}

func Preview(ctx context.Context, input sourceconfig.Input, secret string) (PreviewResult, error) {
	status := 0
	fetch := func(ctx context.Context, httpConfig generic.HTTPConfig, pagination generic.PaginationConfig) ([]any, bool, error) {
		records, complete, code, err := generic.PreviewRecordsWithStatus(ctx, httpConfig, pagination)
		status = code
		return records, complete, err
	}
	result, err := previewWithFetcher(ctx, input, secret, fetch)
	result.HTTPStatus = status
	return result, err
}

type previewFetcher func(context.Context, generic.HTTPConfig, generic.PaginationConfig) ([]any, bool, error)

func previewWithFetcher(ctx context.Context, input sourceconfig.Input, secret string, fetch previewFetcher) (PreviewResult, error) {
	if err := validateSource(input, secret); err != nil {
		return PreviewResult{}, err
	}
	if fetch == nil {
		return PreviewResult{}, errors.New("preview fetcher is unavailable")
	}
	source := sourceconfig.Source{
		SourceKey: input.SourceKey, EndpointURL: input.EndpointURL,
		AuthType: input.AuthType, AuthName: input.AuthName,
		QueryParams: input.QueryParams, ResponsePath: input.ResponsePath,
		Pagination: input.Pagination, Mapping: input.Mapping,
		Defaults: input.Defaults, PriceUnit: input.PriceUnit,
	}
	config, err := configured(source, secret)
	if err != nil {
		return PreviewResult{}, err
	}
	records, complete, err := fetch(ctx, config.http, config.pagination)
	if err != nil {
		return PreviewResult{}, err
	}
	result := PreviewResult{ConnectionOK: true, HTTPStatus: 200, Received: len(records), Complete: complete, Errors: []PreviewError{}, Warnings: []PreviewError{}, Preview: []PreviewEvent{}}
	counts := map[string]int{}
	warningCounts := map[string]int{}
	resourceCounts := map[string]int{}
	for _, raw := range records {
		object, ok := raw.(map[string]any)
		if !ok {
			result.Invalid++
			counts["record_not_object"]++
			continue
		}
		event, err := generic.Normalize(config.mapping, object)
		if err != nil {
			result.Invalid++
			counts[previewCode(err)]++
			continue
		}
		sanitizeOptionalResources(&event)
		result.Valid++
		for _, image := range event.Images {
			if hostname, err := resourcedomains.ResourceHostname(image.URL); err == nil {
				resourceCounts[string(resourcedomains.PurposeImage)+"\x00"+hostname]++
			}
		}
		if event.TicketURL != nil {
			if hostname, err := resourcedomains.ResourceHostname(*event.TicketURL); err == nil {
				resourceCounts[string(resourcedomains.PurposeTicket)+"\x00"+hostname]++
			}
		}
		if len(result.Preview) < previewSize {
			result.Preview = append(result.Preview, PreviewEvent{
				Title: event.Title, StartsAt: event.StartsAt,
				Venue: event.Venue.Name, PriceFromMinor: event.PriceFromMinor,
			})
		}
	}
	for key, count := range resourceCounts {
		parts := strings.SplitN(key, "\x00", 2)
		purpose := resourcedomains.Purpose(parts[0])
		result.ResourceDomains = append(result.ResourceDomains, ResourceDomain{Hostname: parts[1], Purpose: purpose, Count: count})
		if purpose == resourcedomains.PurposeImage {
			warningCounts["image_host_not_allowed"] += count
		} else {
			warningCounts["ticket_host_not_allowed"] += count
		}
	}
	sort.Slice(result.ResourceDomains, func(i, j int) bool {
		if result.ResourceDomains[i].Purpose != result.ResourceDomains[j].Purpose {
			return result.ResourceDomains[i].Purpose < result.ResourceDomains[j].Purpose
		}
		return result.ResourceDomains[i].Hostname < result.ResourceDomains[j].Hostname
	})
	for code, count := range counts {
		result.Errors = append(result.Errors, PreviewError{Code: code, Count: count})
	}
	for code, count := range warningCounts {
		result.Warnings = append(result.Warnings, PreviewError{Code: code, Count: count})
	}
	sort.Slice(result.Errors, func(i, j int) bool { return result.Errors[i].Code < result.Errors[j].Code })
	sort.Slice(result.Warnings, func(i, j int) bool { return result.Warnings[i].Code < result.Warnings[j].Code })
	return result, nil
}

func applyApprovedDomains(result *PreviewResult, allowed func(string, resourcedomains.Purpose) (bool, error)) {
	warningCounts := map[string]int{}
	for index := range result.ResourceDomains {
		approved, err := allowed(result.ResourceDomains[index].Hostname, result.ResourceDomains[index].Purpose)
		result.ResourceDomains[index].Approved = err == nil && approved
		if !result.ResourceDomains[index].Approved {
			code := "ticket_host_not_allowed"
			if result.ResourceDomains[index].Purpose == resourcedomains.PurposeImage {
				code = "image_host_not_allowed"
			}
			warningCounts[code] += result.ResourceDomains[index].Count
		}
	}
	result.Warnings = result.Warnings[:0]
	for code, count := range warningCounts {
		result.Warnings = append(result.Warnings, PreviewError{Code: code, Count: count})
	}
	sort.Slice(result.Warnings, func(i, j int) bool { return result.Warnings[i].Code < result.Warnings[j].Code })
}

func previewCode(err error) string {
	text := err.Error()
	for _, field := range []string{"external_id", "title", "starts_at", "venue.name", "ends_at", "price_from", "price_to"} {
		if strings.HasPrefix(text, field+" ") {
			return "invalid_" + strings.ReplaceAll(field, ".", "_")
		}
	}
	return "invalid_record"
}

func importRecords(ctx context.Context, cityID uuid.UUID, source sourceconfig.Source, secret string, store providers.SyncStore) (stats providers.ImportStats, importErr error) {
	return importRecordsWithFetcher(ctx, cityID, source, secret, store, generic.FetchRecordsWithStats)
}

type importFetcher func(context.Context, generic.HTTPConfig, generic.PaginationConfig) ([]any, int, error)

func importRecordsWithFetcher(ctx context.Context, cityID uuid.UUID, source sourceconfig.Source, secret string, store providers.SyncStore, fetch importFetcher) (stats providers.ImportStats, importErr error) {
	config, err := configured(source, secret)
	if err != nil {
		return stats, err
	}
	if fetch == nil {
		return stats, errors.New("import fetcher is unavailable")
	}
	ingestion, err := providers.NewIngestion(cityID, store, nil)
	if err != nil {
		return stats, err
	}
	now := time.Now().UTC()
	runID, err := store.BeginSyncRun(ctx, providers.SyncRunStart{
		Provider: source.SourceKey, CityID: cityID,
		WindowStart: now, WindowEnd: now, UpsertOnly: true,
	})
	if err != nil {
		return stats, errors.New("begin generic sync run failed")
	}
	defer func() {
		stats.SyncRunID = runID
		reconciled, finishErr := providers.FinalizeSyncRun(ctx, store, runID, stats, importErr)
		stats.Reconciled = reconciled
		if finishErr != nil {
			importErr = errors.Join(importErr, errors.New("finish generic sync run failed"))
		}
	}()
	records, pages, err := fetch(ctx, config.http, config.pagination)
	for range pages {
		ingestion.AddPage()
	}
	if err != nil {
		return ingestion.Stats(), err
	}
	ingestion.AddFetched(len(records))
	ingestion.AddMatched(len(records))
	invalid := 0
	for _, raw := range records {
		object, ok := raw.(map[string]any)
		if !ok {
			invalid++
			ingestion.AddSkipped(1)
			continue
		}
		event, err := generic.Normalize(config.mapping, object)
		if err != nil {
			invalid++
			ingestion.AddSkipped(1)
			continue
		}
		sanitizeOptionalResources(&event)
		if err := ingestion.Persist(ctx, event); err != nil {
			stats = ingestion.Stats()
			stats.Errors += invalid
			return stats, err
		}
	}
	stats = ingestion.Stats()
	stats.Errors += invalid
	return stats, nil
}

// Invalid optional URLs are dropped as optional resource data. This keeps a
// bad provider image or ticket field from failing an otherwise valid event.
func sanitizeOptionalResources(event *providers.NormalizedEvent) {
	if event == nil {
		return
	}
	images := event.Images[:0]
	for _, image := range event.Images {
		if _, err := resourcedomains.ResourceHostname(image.URL); err == nil {
			images = append(images, image)
		}
	}
	event.Images = images
	if event.TicketURL != nil {
		if _, err := resourcedomains.ResourceHostname(*event.TicketURL); err != nil {
			event.TicketURL = nil
			event.TicketAvailable = false
		}
	}
}
