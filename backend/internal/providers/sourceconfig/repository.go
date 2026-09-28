package sourceconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

var (
	ErrNotFound                          = errors.New("event source not found")
	ErrIdentityImmutable                 = errors.New("event source identity is immutable")
	ErrMappingImmutable                  = errors.New("event source external_id mapping is immutable after import")
	ErrSecretRequiredForConnectionChange = errors.New("authentication secret is required when connection settings change")
)

type Defaults struct {
	Category string `json:"category"`
	Timezone string `json:"timezone"`
	Currency string `json:"currency"`
	Status   string `json:"status"`
}

// Input is the write model used by the internal API. Secret is write-only;
// callers must not serialize it back to clients.
type Input struct {
	SourceKey       string            `json:"source_key"`
	Name            string            `json:"name"`
	Enabled         bool              `json:"enabled"`
	EndpointURL     string            `json:"endpoint_url"`
	AuthType        string            `json:"auth_type"`
	AuthName        string            `json:"auth_name,omitempty"`
	Secret          string            `json:"auth_secret,omitempty"`
	QueryParams     map[string]string `json:"query_params,omitempty"`
	ResponsePath    string            `json:"response_path"`
	Pagination      json.RawMessage   `json:"pagination"`
	Mapping         json.RawMessage   `json:"mapping"`
	TransformConfig json.RawMessage   `json:"transform_config,omitempty"`
	Defaults        Defaults          `json:"defaults"`
	PriceUnit       string            `json:"price_unit"`
}

// Source contains persisted source configuration. It never includes a secret.
type Source struct {
	ID               uuid.UUID         `json:"id"`
	SourceKey        string            `json:"source_key"`
	Name             string            `json:"name"`
	Enabled          bool              `json:"enabled"`
	EndpointURL      string            `json:"endpoint_url"`
	AuthType         string            `json:"auth_type"`
	AuthName         string            `json:"auth_name,omitempty"`
	SecretConfigured bool              `json:"secret_configured"`
	QueryParams      map[string]string `json:"query_params"`
	ResponsePath     string            `json:"response_path"`
	Pagination       json.RawMessage   `json:"pagination"`
	Mapping          json.RawMessage   `json:"mapping"`
	Defaults         Defaults          `json:"defaults"`
	PriceUnit        string            `json:"price_unit"`
	MappingLocked    bool              `json:"mapping_locked"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	secretCiphertext []byte
	keyVersion       int16
}

type Repository struct {
	db    *store.Pool
	codec *SecretCodec
}

func NewRepository(db *store.Pool, codec *SecretCodec) (*Repository, error) {
	if db == nil || codec == nil {
		return nil, errors.New("event source repository dependencies are required")
	}
	return &Repository{db: db, codec: codec}, nil
}

func (r *Repository) Create(ctx context.Context, input Input) (Source, error) {
	if err := validateInput(input, true); err != nil {
		return Source{}, err
	}
	id := uuid.New()
	var ciphertext []byte
	var err error
	if input.Secret != "" {
		ciphertext, err = r.codec.Seal(id, input.Secret)
		if err != nil {
			return Source{}, err
		}
	}
	queryRaw := json.RawMessage(`{}`)
	if input.QueryParams != nil {
		queryRaw, _ = json.Marshal(input.QueryParams)
	}
	query, err := jsonObject(queryRaw, map[string]string{})
	if err != nil {
		return Source{}, err
	}
	pagination, err := jsonObject(input.Pagination, map[string]string{"mode": "none"})
	if err != nil {
		return Source{}, err
	}
	mapping, err := jsonObject(input.Mapping, map[string]string{})
	if err != nil {
		return Source{}, err
	}
	transforms := []byte(`{}`)
	_, err = r.db.Exec(ctx, `INSERT INTO event_sources (
		id, source_key, name, enabled, endpoint_url, auth_type, auth_name, auth_secret_ciphertext,
		key_version, query_config, response_path, mapping_config, transform_config, pagination_config,
		default_category, default_timezone, default_currency, default_status, price_unit)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11,$12::jsonb,$13::jsonb,$14::jsonb,$15,$16,$17,$18,$19)`,
		id, input.SourceKey, input.Name, input.Enabled, input.EndpointURL, input.AuthType, input.AuthName,
		ciphertext, r.codec.version, query, input.ResponsePath, mapping, transforms, pagination,
		input.Defaults.Category, input.Defaults.Timezone, input.Defaults.Currency, input.Defaults.Status, input.PriceUnit)
	if err != nil {
		return Source{}, fmt.Errorf("create event source: %w", err)
	}
	return r.Get(ctx, id)
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (Source, error) {
	if id == uuid.Nil {
		return Source{}, ErrNotFound
	}
	return scanSource(r.db.QueryRow(ctx, sourceSelect+` WHERE id=$1`, id))
}

func (r *Repository) List(ctx context.Context) ([]Source, error) {
	rows, err := r.db.Query(ctx, sourceSelect+` ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("list event sources: %w", err)
	}
	defer rows.Close()
	result := make([]Source, 0)
	for rows.Next() {
		source, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list event sources: %w", err)
	}
	return result, nil
}

// GetForExecution returns decrypted credentials separately from the JSON-safe
// source model so handlers cannot accidentally expose the secret.
func (r *Repository) GetForExecution(ctx context.Context, id uuid.UUID) (Source, string, error) {
	source, err := r.Get(ctx, id)
	if err != nil {
		return Source{}, "", err
	}
	if !source.SecretConfigured {
		return source, "", nil
	}
	secret, err := r.codec.Open(source.ID, source.secretCiphertext, source.keyVersion)
	if err != nil {
		return Source{}, "", err
	}
	return source, secret, nil
}

func (r *Repository) Update(ctx context.Context, id uuid.UUID, input Input) (Source, error) {
	if id == uuid.Nil {
		return Source{}, ErrNotFound
	}
	if err := validateInput(input, false); err != nil {
		return Source{}, err
	}
	previous, err := r.Get(ctx, id)
	if err != nil {
		return Source{}, err
	}
	if input.SourceKey != previous.SourceKey {
		return Source{}, ErrIdentityImmutable
	}
	if previous.MappingLocked && !sameExternalIDMapping(previous.Mapping, input.Mapping) {
		return Source{}, ErrMappingImmutable
	}
	if previous.SecretConfigured && input.AuthType != "none" && input.Secret == "" && !sameSecretConnection(previous, input) {
		return Source{}, ErrSecretRequiredForConnectionChange
	}
	queryRaw := json.RawMessage(`{}`)
	if input.QueryParams != nil {
		queryRaw, _ = json.Marshal(input.QueryParams)
	}
	query, err := jsonObject(queryRaw, map[string]string{})
	if err != nil {
		return Source{}, err
	}
	pagination, err := jsonObject(input.Pagination, map[string]string{"mode": "none"})
	if err != nil {
		return Source{}, err
	}
	mapping, err := jsonObject(input.Mapping, map[string]string{})
	if err != nil {
		return Source{}, err
	}
	transforms := []byte(`{}`)
	var ciphertext any = previous.secretCiphertext
	if input.AuthType == "none" {
		ciphertext = nil
	} else if input.Secret != "" {
		ciphertext, err = r.codec.Seal(id, input.Secret)
		if err != nil {
			return Source{}, err
		}
	} else if !previous.SecretConfigured {
		return Source{}, errors.New("authentication secret is required")
	}
	_, err = r.db.Exec(ctx, `UPDATE event_sources SET name=$2, enabled=$3, endpoint_url=$4, auth_type=$5, auth_name=$6,
		auth_secret_ciphertext=$7, key_version=$8, query_config=$9::jsonb, response_path=$10, mapping_config=$11::jsonb,
		transform_config=$12::jsonb, pagination_config=$13::jsonb, default_category=$14, default_timezone=$15,
		default_currency=$16, default_status=$17, price_unit=$18 WHERE id=$1`,
		id, input.Name, input.Enabled, input.EndpointURL, input.AuthType, input.AuthName, ciphertext, r.codec.version,
		query, input.ResponsePath, mapping, transforms, pagination, input.Defaults.Category, input.Defaults.Timezone,
		input.Defaults.Currency, input.Defaults.Status, input.PriceUnit)
	if err != nil {
		return Source{}, fmt.Errorf("update event source: %w", err)
	}
	return r.Get(ctx, id)
}

func sameSecretConnection(previous Source, input Input) bool {
	return previous.EndpointURL == input.EndpointURL &&
		previous.AuthType == input.AuthType &&
		previous.AuthName == input.AuthName &&
		maps.Equal(previous.QueryParams, input.QueryParams)
}

func (r *Repository) MarkMappingLocked(ctx context.Context, sourceKey string) error {
	command, err := r.db.Exec(ctx, `UPDATE event_sources SET mapping_locked=true WHERE source_key=$1`, strings.TrimSpace(sourceKey))
	if err != nil {
		return fmt.Errorf("lock event source mapping: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const sourceSelect = `SELECT id,source_key,name,enabled,endpoint_url,auth_type,auth_name,
 auth_secret_ciphertext IS NOT NULL, query_config, response_path, pagination_config, mapping_config,
 default_category, default_timezone, default_currency, default_status, price_unit,
 mapping_locked, created_at, updated_at, auth_secret_ciphertext, key_version FROM event_sources`

type rowScanner interface{ Scan(...any) error }

func scanSource(row rowScanner) (Source, error) {
	var s Source
	var query, pagination, mapping []byte
	var defaultsCategory, defaultsTimezone, defaultsCurrency, defaultsStatus string
	err := row.Scan(&s.ID, &s.SourceKey, &s.Name, &s.Enabled, &s.EndpointURL, &s.AuthType, &s.AuthName, &s.SecretConfigured,
		&query, &s.ResponsePath, &pagination, &mapping, &defaultsCategory, &defaultsTimezone, &defaultsCurrency, &defaultsStatus,
		&s.PriceUnit, &s.MappingLocked, &s.CreatedAt, &s.UpdatedAt, &s.secretCiphertext, &s.keyVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Source{}, ErrNotFound
	}
	if err != nil {
		return Source{}, fmt.Errorf("read event source: %w", err)
	}
	s.QueryParams = make(map[string]string)
	if err := json.Unmarshal(query, &s.QueryParams); err != nil {
		return Source{}, errors.New("stored event source query config is invalid")
	}
	s.Pagination, s.Mapping = pagination, mapping
	s.Defaults = Defaults{Category: defaultsCategory, Timezone: defaultsTimezone, Currency: defaultsCurrency, Status: defaultsStatus}
	return s, nil
}

func validateInput(input Input, create bool) error {
	if strings.TrimSpace(input.SourceKey) == "" || strings.TrimSpace(input.SourceKey) == "demo" || strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.EndpointURL) == "" {
		return errors.New("event source identity, name, and endpoint are required")
	}
	switch input.AuthType {
	case "none":
	case "bearer":
	case "api_key_header", "api_key_query":
		if strings.TrimSpace(input.AuthName) == "" {
			return errors.New("authentication name is required")
		}
	default:
		return errors.New("event source authentication type is invalid")
	}
	if input.AuthType != "none" && create && input.Secret == "" {
		return errors.New("authentication secret is required")
	}
	if input.Defaults.Category == "" || input.Defaults.Timezone == "" || input.Defaults.Currency == "" || input.Defaults.Status == "" {
		return errors.New("event source defaults are required")
	}
	if input.PriceUnit != "major" && input.PriceUnit != "minor" {
		return errors.New("event source price unit is invalid")
	}
	for name, value := range map[string]json.RawMessage{"query_params": input.QueryParamsRaw(), "pagination": input.Pagination, "mapping": input.Mapping, "transform_config": input.TransformConfig} {
		if len(value) > 0 && !json.Valid(value) {
			return fmt.Errorf("event source %s must be valid JSON", name)
		}
	}
	if len(input.TransformConfig) > 0 {
		var transformConfig map[string]json.RawMessage
		if err := json.Unmarshal(input.TransformConfig, &transformConfig); err != nil || transformConfig == nil || len(transformConfig) != 0 {
			return errors.New("transform_config is unsupported; use field mapping transforms")
		}
	}
	return nil
}

func (i Input) QueryParamsRaw() json.RawMessage {
	value, _ := json.Marshal(i.QueryParams)
	return value
}
func jsonObject(value json.RawMessage, fallback any) ([]byte, error) {
	if len(value) == 0 {
		b, err := json.Marshal(fallback)
		return b, err
	}
	if !json.Valid(value) {
		return nil, errors.New("event source configuration must be valid JSON")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(value, &object); err != nil || object == nil {
		return nil, errors.New("event source configuration must be a JSON object")
	}
	return value, nil
}
func sameExternalIDMapping(a, b json.RawMessage) bool {
	var left, right map[string]json.RawMessage
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	var leftValue, rightValue any
	if len(left["external_id"]) == 0 || len(right["external_id"]) == 0 {
		return len(left["external_id"]) == len(right["external_id"])
	}
	if json.Unmarshal(left["external_id"], &leftValue) != nil || json.Unmarshal(right["external_id"], &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}
