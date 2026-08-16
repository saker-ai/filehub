package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/saker-ai/filehub/pkg/config"
	"github.com/saker-ai/filehub/pkg/store"
)

// TestCollectExpiredSkipsWorkspaceReferencedAsset verifies FH-11 on the GC
// path: an expired asset that a workspace revision references must not have
// its blob or metadata deleted.
func TestCollectExpiredSkipsWorkspaceReferencedAsset(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.DSN = "sqlite://" + filepath.Join(dir, "filehub.db")
	cfg.Storage.Backend = config.BackendOSFS
	cfg.Storage.DataDir = filepath.Join(dir, "objects")
	cfg.WebEnabled = false
	cfg.Workspaces.Enabled = true
	cfg.APIKeyAuthEnabled = true
	cfg.APIKeys = []string{"test-key"}

	srv, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.db.Close() })

	// Create an asset directly in the store and blob storage.
	storageKey := "default/general/asset-wsref/file.txt"
	if _, err := srv.blobs.Put(ctx, storageKey, bytes.NewBufferString("workspace referenced")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Create an asset with a future expiry; the commit references it and
	// must clear the expiry eligibility.
	future := time.Now().Add(24 * time.Hour)
	asset := &store.Asset{
		ID: "asset-wsref", TenantID: "default", Purpose: "general", Filename: "file.txt",
		ContentType: "text/plain", Bytes: 20, StorageKey: storageKey,
		Checksum: "sha256:deadbeef", Status: "ready", ExpiresAt: &future,
	}
	if err := srv.db.Create(ctx, asset); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Reference it from a workspace revision via the HTTP commit path.
	rec := request(t, srv, http.MethodPost, "/v1/workspaces", `{"name":"gc-ws"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create workspace status=%d body=%s", rec.Code, rec.Body.String())
	}
	wsID := jsonField(t, rec.Body.String(), "id")

	commitBody := `{"device_id":"device-1","session_id":"s","operations":[{"kind":"put","path":"file.txt","asset_id":"asset-wsref","base_revision_id":""}]}`
	rec = requestWithKey(t, srv, http.MethodPost, "/v1/workspaces/"+wsID+"/commits", commitBody, "req-gc")
	if rec.Code != http.StatusOK {
		t.Fatalf("commit status=%d body=%s", rec.Code, rec.Body.String())
	}

	// The commit must have cleared the expiry eligibility.
	cleared, err := srv.db.Get(ctx, "default", "asset-wsref")
	if err != nil {
		t.Fatalf("Get after commit: %v", err)
	}
	if cleared.ExpiresAt != nil {
		t.Fatalf("expires_at not cleared after workspace reference: %v", cleared.ExpiresAt)
	}

	// Force expiry again and run GC: the asset must survive.
	if err := srv.db.UpdateStatus(ctx, "asset-wsref", "ready"); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	exp := time.Now().Add(-time.Hour)
	updated := *cleared
	updated.ExpiresAt = &exp
	if err := srv.db.Update(ctx, &updated); err != nil {
		t.Fatalf("Update expiry: %v", err)
	}

	srv.collectExpired(ctx)

	if _, err := srv.db.Get(ctx, "default", "asset-wsref"); err != nil {
		t.Fatalf("workspace-referenced asset deleted by GC: %v", err)
	}
	rc, err := srv.blobs.Get(ctx, storageKey)
	if err != nil {
		t.Fatalf("workspace-referenced blob deleted by GC: %v", err)
	}
	_ = rc.Close()
}

func request(t *testing.T, srv *Server, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	return requestWithKey(t, srv, method, target, body, "")
}

func requestWithKey(t *testing.T, srv *Server, method, target, body, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	}
	var req *http.Request
	if reader != nil {
		req = httptest.NewRequest(method, target, reader)
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	return rec
}

func jsonField(t *testing.T, body, field string) string {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	v, _ := out[field].(string)
	if v == "" {
		t.Fatalf("field %q missing in %s", field, body)
	}
	return v
}
