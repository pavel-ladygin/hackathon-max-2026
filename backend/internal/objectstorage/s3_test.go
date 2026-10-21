package objectstorage

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestS3PathStylePutGetDelete(t *testing.T) {
	var requests []string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=test-access/") {
			t.Errorf("missing SigV4 auth")
		}
		status, body := http.StatusOK, ""
		headers := make(http.Header)
		switch {
		case r.URL.Query().Has("acl"):
			body = `<AccessControlPolicy><Owner><ID>owner</ID></Owner><AccessControlList></AccessControlList></AccessControlPolicy>`
		case r.URL.Query().Has("policyStatus"):
			body = `<PolicyStatus><IsPublic>false</IsPublic></PolicyStatus>`
		case r.Method == http.MethodPut:
			if r.Header.Get("X-Amz-Sdk-Checksum-Algorithm") != "" || r.Header.Get("X-Amz-Checksum-Crc32") != "" {
				t.Error("optional AWS checksum extension sent to compatible endpoint")
			}
			if got := r.Header.Get("X-Amz-Acl"); got != "private" {
				t.Errorf("PUT ACL = %q, want private", got)
			}
			data, err := io.ReadAll(r.Body)
			if err != nil || string(data) != "image" {
				t.Errorf("PUT body = %q, %v", data, err)
			}
		case r.Method == http.MethodGet:
			headers.Set("Content-Type", "image/png")
			body = "image"
		case r.Method == http.MethodDelete:
			status = http.StatusNoContent
		}
		return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	client, err := New("https://s3.example.test", "us-east-1", "bucket", "test-access", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	client.httpClient.Transport = transport
	ctx := context.Background()
	if err := client.Put(ctx, "event-submissions/123", []byte("image"), "image/png"); err != nil {
		t.Fatal(err)
	}
	body, kind, err := client.Get(ctx, "event-submissions/123")
	if err != nil || string(body) != "image" || kind != "image/png" {
		t.Fatalf("Get = %q, %q, %v", body, kind, err)
	}
	if err := client.Delete(ctx, "event-submissions/123"); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 5 || !strings.Contains(requests[0], "/bucket?acl=") || !strings.Contains(requests[1], "/bucket?policyStatus=") || !strings.Contains(requests[2], "/bucket/event-submissions/123") {
		t.Fatalf("unexpected request sequence: %v", requests)
	}
}

func TestPrivateBucketWithoutPolicyAllowsUploadButOtherPolicyErrorsFail(t *testing.T) {
	for _, code := range []string{"NoSuchBucketPolicy", "AccessDenied", "NotImplemented", "NoSuchBucket"} {
		t.Run(code, func(t *testing.T) {
			put := false
			client, err := New("https://s3.example.test", "us-east-1", "bucket", "test-access", "test-secret")
			if err != nil {
				t.Fatal(err)
			}
			client.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				status, body := http.StatusOK, ""
				switch {
				case r.URL.Query().Has("acl"):
					body = `<AccessControlPolicy><AccessControlList></AccessControlList></AccessControlPolicy>`
				case r.URL.Query().Has("policyStatus"):
					status, body = http.StatusNotFound, `<Error><Code>`+code+`</Code><Message>Policy unavailable</Message></Error>`
				case r.Method == http.MethodPut:
					put = true
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			err = client.Put(context.Background(), "image", []byte("image"), "image/png")
			if code == "NoSuchBucketPolicy" {
				if err != nil || !put {
					t.Fatalf("private bucket without policy: upload=%v, error=%v", put, err)
				}
			} else if err == nil || put {
				t.Fatalf("policy error %s must block upload: upload=%v, error=%v", code, put, err)
			}
		})
	}
}

func TestS3CustomPrefixIsAppliedOnce(t *testing.T) {
	client, err := NewWithPrefix("https://s3.example.test", "us-east-1", "bucket", "test-access", "test-secret", "custom/prefix")
	if err != nil {
		t.Fatal(err)
	}
	if got := client.ObjectKey("event-submissions/123"); got != "custom/prefix/event-submissions/123" {
		t.Fatalf("ObjectKey = %q", got)
	}
	if got := client.ObjectKey("custom/prefix/event-submissions/123"); got != "custom/prefix/event-submissions/123" {
		t.Fatalf("ObjectKey duplicated custom prefix: %q", got)
	}
}

func TestS3RefusesPublicBucketACL(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `<AccessControlPolicy><Owner><ID>owner</ID></Owner><AccessControlList><Grant><Grantee xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="Group"><URI>http://acs.amazonaws.com/groups/global/AllUsers</URI></Grantee><Permission>READ</Permission></Grant></AccessControlList></AccessControlPolicy>`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	client, err := New("https://s3.example.test", "us-east-1", "bucket", "test-access", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	client.httpClient.Transport = transport
	if err := client.Put(context.Background(), "event-submissions/123", []byte("image"), "image/png"); err == nil || !strings.Contains(err.Error(), "public access") {
		t.Fatalf("Put on public bucket returned %v", err)
	}
}

func TestS3RejectsRedirects(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("Location", "https://attacker.example/redirect")
		return &http.Response{StatusCode: http.StatusFound, Header: header, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	client, err := New("https://s3.example.test", "us-east-1", "bucket", "test-access", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	client.httpClient.Transport = transport
	if err := client.Put(context.Background(), "event-submissions/123", []byte("image"), "image/png"); err == nil {
		t.Fatal("Put followed or accepted an HTTP redirect")
	}
}

func TestNewFromEnvRequiresCompleteCredentials(t *testing.T) {
	for _, name := range []string{"REG_S3_BUCKET", "REG_S3_ACCESS_KEY_ID", "REG_S3_SECRET_ACCESS_KEY", "REG_S3_ENDPOINT", "REG_S3_REGION", "REG_S3_PREFIX"} {
		t.Setenv(name, "")
	}
	if _, err := NewFromEnv(); err != ErrNotConfigured {
		t.Fatalf("empty config error = %v", err)
	}
	t.Setenv("REG_S3_BUCKET", "hackathon-max")
	if _, err := NewFromEnv(); err == nil || err == ErrNotConfigured {
		t.Fatalf("partial config error = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
