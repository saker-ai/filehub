package storage

import (
	"bytes"
	"errors"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/saker-ai/filehub/pkg/config"
)

func TestPromoteMovesObjectAndIsIdempotent(t *testing.T) {
	cfg := config.Defaults()
	cfg.Storage.Backend = config.BackendMemFS
	store, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(t.Context(), "_uploads/upl-1/file.txt", bytes.NewBufferString("payload")); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := store.Promote(t.Context(), "_uploads/upl-1/file.txt", "tenant/general/asset-1/file.txt"); err != nil {
			t.Fatalf("Promote attempt %d: %v", attempt+1, err)
		}
	}
	rc, err := store.Get(t.Context(), "tenant/general/asset-1/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(rc)
	closeErr := rc.Close()
	if readErr != nil || closeErr != nil || string(data) != "payload" {
		t.Fatalf("promoted object data=%q readErr=%v closeErr=%v", data, readErr, closeErr)
	}
	if _, err := store.Get(t.Context(), "_uploads/upl-1/file.txt"); err == nil || !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("staged object lookup error = %v, want not found", err)
	}
}

func TestPresignObjectURLUsesPublicEndpoint(t *testing.T) {
	tests := []struct {
		name       string
		backend    string
		endpoint   string
		public     string
		wantHost   string
		wantPrefix string
	}{
		{
			name:       "S3 path-style endpoint",
			backend:    config.BackendS3,
			endpoint:   "http://s3.internal:9000",
			public:     "https://assets.example.com",
			wantHost:   "assets.example.com",
			wantPrefix: "/test-bucket/",
		},
		{
			name:       "OSS virtual-host endpoint",
			backend:    config.BackendOSS,
			endpoint:   "http://oss.internal:9000",
			public:     "https://oss-cn-shanghai.aliyuncs.com",
			wantHost:   "test-bucket.oss-cn-shanghai.aliyuncs.com",
			wantPrefix: "/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Storage.Backend = tt.backend
			cfg.Storage.S3Endpoint = tt.endpoint
			cfg.Storage.S3PublicEndpoint = tt.public
			cfg.Storage.S3Region = "us-east-1"
			cfg.Storage.S3Bucket = "test-bucket"
			cfg.Storage.S3AccessKey = "test-key"
			cfg.Storage.S3SecretKey = "test-secret"

			store, err := New(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := store.PresignObjectURL(t.Context(), "assets/example.txt", cfg.PresignTTL)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := url.Parse(raw)
			if err != nil {
				t.Fatalf("parse presigned URL: %v", err)
			}
			if parsed.Scheme != "https" || parsed.Host != tt.wantHost || !strings.HasPrefix(parsed.Path, tt.wantPrefix) {
				t.Fatalf("presigned URL = %q, want https://%s%s...", raw, tt.wantHost, tt.wantPrefix)
			}
			if got := parsed.Query().Get("X-Amz-Expires"); got != "604800" {
				t.Fatalf("X-Amz-Expires = %q, want 604800", got)
			}
		})
	}
}
