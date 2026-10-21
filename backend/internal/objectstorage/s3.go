// Package objectstorage implements the private S3-compatible object store used
// for user supplied event images.
package objectstorage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

const maxObjectSize = 10 << 20

var ErrNotConfigured = errors.New("REG.RU S3 storage is not configured")

// Store is the minimal object-storage contract needed by event image uploads.
type Store interface {
	Put(context.Context, string, []byte, string) error
	Get(context.Context, string) ([]byte, string, error)
	Delete(context.Context, string) error
	Available() bool
}

// S3Store uses the AWS maintained Go SDK v2 with S3 path-style addressing.
// Bucket privacy is verified before any object operation; failures are closed.
type S3Store struct {
	client     *s3.Client
	httpClient *http.Client
	bucket     string
	prefix     string

	verifyMu   sync.Mutex
	verifiedAt time.Time
}

// New constructs a path-style S3 client using the dedicated event-submissions
// object prefix. Endpoint and region default to REG.RU's S3 endpoint and the
// conventional us-east-1 signing region respectively.
func New(endpoint, region, bucket, access, secret string) (*S3Store, error) {
	return NewWithPrefix(endpoint, region, bucket, access, secret, "event-submissions")
}

// NewWithPrefix constructs a client with an optional bucket key prefix.
func NewWithPrefix(endpoint, region, bucket, access, secret, prefix string) (*S3Store, error) {
	if strings.TrimSpace(bucket) == "" && strings.TrimSpace(access) == "" && strings.TrimSpace(secret) == "" {
		return nil, ErrNotConfigured
	}
	if strings.TrimSpace(bucket) == "" || strings.TrimSpace(access) == "" || strings.TrimSpace(secret) == "" {
		return nil, errors.New("S3 bucket and both credentials must be configured together")
	}
	if strings.TrimSpace(endpoint) == "" {
		endpoint = "https://s3.regru.cloud"
	}
	if strings.TrimSpace(region) == "" {
		region = "us-east-1"
	}
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("S3 endpoint must be an HTTPS URL without credentials, query, or fragment")
	}
	if strings.Contains(bucket, "/") || strings.Contains(bucket, "\\") || strings.Contains(bucket, "..") {
		return nil, errors.New("invalid S3 bucket name")
	}
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		prefix = "event-submissions"
	}
	for _, segment := range strings.Split(prefix, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.Contains(segment, "\\") {
			return nil, errors.New("invalid S3 object prefix")
		}
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	httpClient := &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	client := s3.New(s3.Options{
		Region:       region,
		BaseEndpoint: aws.String(parsed.String()),
		UsePathStyle: true,
		HTTPClient:   httpClient,
		Credentials:  aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(access, secret, "")),
		// Keep optional AWS checksum extensions out of requests to an
		// S3-compatible service; required checksums remain enabled.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	return &S3Store{client: client, httpClient: httpClient, bucket: bucket, prefix: prefix}, nil
}

// NewFromEnv constructs a client from REG_S3_* variables. Bucket and both
// credentials are required; endpoint and region have documented defaults.
func NewFromEnv() (*S3Store, error) {
	return NewWithPrefix(
		strings.TrimSpace(getenv("REG_S3_ENDPOINT")),
		strings.TrimSpace(getenv("REG_S3_REGION")),
		strings.TrimSpace(getenv("REG_S3_BUCKET")),
		strings.TrimSpace(getenv("REG_S3_ACCESS_KEY_ID")),
		strings.TrimSpace(getenv("REG_S3_SECRET_ACCESS_KEY")),
		strings.TrimSpace(getenv("REG_S3_PREFIX")),
	)
}

func (s *S3Store) Available() bool { return s != nil && s.client != nil }

func (s *S3Store) Put(ctx context.Context, key string, body []byte, contentType string) error {
	if !s.Available() {
		return ErrNotConfigured
	}
	if len(body) == 0 || len(body) > maxObjectSize {
		return fmt.Errorf("S3 object size must be between 1 and %d bytes", maxObjectSize)
	}
	if err := s.verifyPrivate(ctx); err != nil {
		return err
	}
	key = s.objectKey(key)
	if key == "" {
		return errors.New("invalid S3 object key")
	}
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key),
		Body: bytes.NewReader(body), ContentLength: aws.Int64(int64(len(body))), ContentType: aws.String(contentType), ACL: types.ObjectCannedACLPrivate,
	})
	if err != nil {
		return fmt.Errorf("S3 put object: %w", err)
	}
	return nil
}

func (s *S3Store) Get(ctx context.Context, key string) ([]byte, string, error) {
	if !s.Available() {
		return nil, "", ErrNotConfigured
	}
	if err := s.verifyPrivate(ctx); err != nil {
		return nil, "", err
	}
	key = s.objectKey(key)
	if key == "" {
		return nil, "", errors.New("invalid S3 object key")
	}
	response, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, "", fmt.Errorf("S3 get object: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxObjectSize+1))
	if err != nil {
		return nil, "", fmt.Errorf("read S3 object: %w", err)
	}
	if len(body) > maxObjectSize {
		return nil, "", errors.New("S3 object exceeds configured size limit")
	}
	return body, aws.ToString(response.ContentType), nil
}

func (s *S3Store) Delete(ctx context.Context, key string) error {
	if !s.Available() {
		return ErrNotConfigured
	}
	if err := s.verifyPrivate(ctx); err != nil {
		return err
	}
	key = s.objectKey(key)
	if key == "" {
		return errors.New("invalid S3 object key")
	}
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}); err != nil {
		return fmt.Errorf("S3 delete object: %w", err)
	}
	return nil
}

func (s *S3Store) verifyPrivate(ctx context.Context) error {
	s.verifyMu.Lock()
	defer s.verifyMu.Unlock()
	if !s.verifiedAt.IsZero() && time.Since(s.verifiedAt) < 30*time.Second {
		return nil
	}
	acl, err := s.client.GetBucketAcl(ctx, &s3.GetBucketAclInput{Bucket: aws.String(s.bucket)})
	if err != nil {
		return fmt.Errorf("verify S3 bucket ACL (requires s3:GetBucketAcl): %w", err)
	}
	for _, grant := range acl.Grants {
		if grant.Grantee == nil || grant.Grantee.URI == nil {
			continue
		}
		uri := aws.ToString(grant.Grantee.URI)
		if strings.Contains(uri, "AllUsers") || strings.Contains(uri, "AuthenticatedUsers") {
			return errors.New("S3 bucket ACL grants public access")
		}
	}
	policy, err := s.client.GetBucketPolicyStatus(ctx, &s3.GetBucketPolicyStatusInput{Bucket: aws.String(s.bucket)})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchBucketPolicy" {
			// A bucket with a private ACL and no policy grants no public access.
			s.verifiedAt = time.Now()
			return nil
		}
		return fmt.Errorf("verify S3 bucket policy status (requires s3:GetBucketPolicyStatus): %w", err)
	}
	if policy.PolicyStatus == nil || policy.PolicyStatus.IsPublic == nil {
		return errors.New("S3 endpoint did not provide bucket policy privacy status")
	}
	if aws.ToBool(policy.PolicyStatus.IsPublic) {
		return errors.New("S3 bucket policy grants public access")
	}
	s.verifiedAt = time.Now()
	return nil
}

// ObjectKey returns the complete persisted bucket key for a caller-owned key.
func (s *S3Store) ObjectKey(key string) string { return s.objectKey(key) }

func (s *S3Store) objectKey(key string) string {
	key = strings.TrimLeft(strings.TrimSpace(key), "/")
	if key == "" || strings.Contains(key, "\\") {
		return ""
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return ""
		}
	}
	if key == s.prefix || strings.HasPrefix(key, s.prefix+"/") {
		return key
	}
	return s.prefix + "/" + key
}

// getenv is a seam to keep env loading testable without exposing credentials.
var getenv = func(key string) string { return os.Getenv(key) }
