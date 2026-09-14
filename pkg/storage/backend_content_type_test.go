package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/saker-ai/filehub/pkg/config"
)

// newTestS3Backend points a real S3 client at a fake endpoint, so the request
// the SDK actually sends can be inspected without credentials or network.
func newTestS3Backend(t *testing.T, handler http.Handler) *s3Backend {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	awsCfg := aws.Config{
		Region:      "cn-shanghai",
		Credentials: credentials.NewStaticCredentialsProvider("ak", "sk", ""),
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
		o.UsePathStyle = true
	})
	return newS3Backend(client, s3.NewPresignClient(client), "saker-media", "saker")
}

// Object stores must record the media type: a presigned link is read without
// going through this service, so nothing can fill the type in later.
func TestS3BackendPutRecordsContentType(t *testing.T) {
	var gotContentType string
	var gotPath string
	backend := newTestS3Backend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotPath = r.URL.Path
		w.Header().Set("ETag", `"etag"`)
		w.WriteHeader(http.StatusOK)
	}))

	written, err := backend.Put(t.Context(), "default/media/asset-1/cat.png", "image/png", bytes.NewBufferString("png bytes"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if written != int64(len("png bytes")) {
		t.Fatalf("written = %d, want %d", written, len("png bytes"))
	}
	if gotContentType != "image/png" {
		t.Fatalf("Content-Type = %q, want %q", gotContentType, "image/png")
	}
	// The configured prefix is applied by the backend, not the caller.
	if !strings.Contains(gotPath, "saker/default/media/asset-1/cat.png") {
		t.Fatalf("path = %q, want it to include the prefixed key", gotPath)
	}
}

func TestS3BackendPutBytesRecordsContentType(t *testing.T) {
	var gotContentType string
	backend := newTestS3Backend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))

	if err := backend.PutBytes(t.Context(), "_thumbs/asset-1/320x240.png", "image/png", []byte("thumb")); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}
	if gotContentType != "image/png" {
		t.Fatalf("Content-Type = %q, want %q", gotContentType, "image/png")
	}
}

// When a caller has no media type to offer, the SDK's own default stands: the
// backend must not send an empty Content-Type header. This is also the value
// that made objects written through this path look like opaque downloads.
func TestS3BackendPutWithoutContentTypeFallsBackToSDKDefault(t *testing.T) {
	var contentType string
	backend := newTestS3Backend(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))

	if _, err := backend.Put(t.Context(), "_chunks/upl-1/part-1", "", bytes.NewBufferString("chunk")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if contentType != "application/octet-stream" {
		t.Fatalf("Content-Type = %q, want the SDK default application/octet-stream", contentType)
	}
}

func TestThumbnailContentType(t *testing.T) {
	for format, want := range map[string]string{
		"jpg":   "image/jpeg",
		"":      "image/jpeg",
		".jpeg": "image/jpeg",
		"png":   "image/png",
		".png":  "image/png",
		"webp":  "image/webp",
		"WEBP":  "image/webp",
	} {
		if got := thumbnailContentType(format); got != want {
			t.Fatalf("thumbnailContentType(%q) = %q, want %q", format, got, want)
		}
	}
}

// The local backend accepts a media type for interface parity but has nowhere
// to keep it; it must still round-trip the bytes.
func TestS2BackendAcceptsContentType(t *testing.T) {
	cfg := config.Defaults()
	cfg.Storage.Backend = config.BackendMemFS
	store, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(t.Context(), "tenant/general/asset-1/a.txt", "text/plain", bytes.NewBufferString("body")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	rc, err := store.Get(t.Context(), "tenant/general/asset-1/a.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "body" {
		t.Fatalf("body = %q, want %q", data, "body")
	}
}
