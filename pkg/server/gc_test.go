package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/saker-ai/filehub/pkg/config"
	"github.com/saker-ai/filehub/pkg/store"
)

func TestCollectExpiredDeletesDirectUploadObject(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.DSN = "sqlite://" + filepath.Join(dir, "filehub.db")
	cfg.Storage.Backend = config.BackendOSFS
	cfg.Storage.DataDir = filepath.Join(dir, "objects")
	cfg.WebEnabled = false

	srv, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.db.Close() })

	const key = "tenant/general/asset-orphan/orphan.txt"
	if _, err := srv.blobs.Put(ctx, key, bytes.NewBufferString("orphan")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	session := &store.UploadSession{
		ID: "upl-orphan", TenantID: "default", AssetID: "asset-orphan", Mode: "direct",
		Filename: "orphan.txt", Purpose: "general", StorageKey: key, Status: "pending",
		CreatedAt: time.Now().Add(-2 * time.Hour), ExpiresAt: time.Now().Add(-time.Hour),
	}
	if err := srv.db.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	srv.collectExpired(ctx)
	rc, err := srv.blobs.Get(ctx, key)
	if err == nil {
		_ = rc.Close()
		t.Fatal("orphan object still exists")
	}
	if !errors.Is(err, os.ErrNotExist) && !strings.Contains(err.Error(), "not exist") {
		t.Fatalf("Get deleted object: %v", err)
	}
	if _, err := srv.db.GetSession(ctx, "default", session.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetSession after GC: %v", err)
	}
	if rendered := srv.metrics.Render(nil); !strings.Contains(rendered, `filehub_direct_uploads_total{mode="direct",outcome="orphan_cleaned"} 1`) {
		t.Fatalf("metrics missing orphan cleanup: %s", rendered)
	}
}

func TestCollectExpiredRetainsSessionWhenObjectCleanupFails(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			http.Error(w, "temporary failure", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></ListBucketResult>`))
	}))
	t.Cleanup(provider.Close)

	cfg := config.Defaults()
	cfg.DSN = "sqlite://" + filepath.Join(t.TempDir(), "filehub.db")
	cfg.Storage.Backend = config.BackendS3
	cfg.Storage.S3Endpoint = provider.URL
	cfg.Storage.S3PublicEndpoint = provider.URL
	cfg.Storage.S3Region = "us-east-1"
	cfg.Storage.S3Bucket = "test-bucket"
	cfg.Storage.S3AccessKey = "test-key"
	cfg.Storage.S3SecretKey = "test-secret"
	cfg.WebEnabled = false

	srv, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.db.Close() })
	session := &store.UploadSession{
		ID: "upl-retry", TenantID: "default", AssetID: "asset-retry", Mode: "direct",
		Filename: "retry.txt", Purpose: "general", StorageKey: "default/_uploads/upl-retry/retry.txt", Status: "pending",
		CreatedAt: time.Now().Add(-2 * time.Hour), ExpiresAt: time.Now().Add(-time.Hour),
	}
	if err := srv.db.CreateSession(t.Context(), session); err != nil {
		t.Fatal(err)
	}

	srv.collectExpired(t.Context())
	retained, err := srv.db.GetSession(t.Context(), "default", session.ID)
	if err != nil {
		t.Fatalf("cleanup session was lost: %v", err)
	}
	if retained.Status != "cleanup_failed" {
		t.Fatalf("session status = %q, want cleanup_failed", retained.Status)
	}
	if rendered := srv.metrics.Render(nil); !strings.Contains(rendered, `filehub_direct_uploads_total{mode="direct",outcome="orphan_cleanup_failed"} 1`) {
		t.Fatalf("metrics missing cleanup failure: %s", rendered)
	}
}
