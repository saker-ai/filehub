package server

import (
	"bytes"
	"context"
	"errors"
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
