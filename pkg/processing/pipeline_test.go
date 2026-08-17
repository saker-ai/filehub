package processing

import (
	"bytes"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/saker-ai/filehub/pkg/config"
	"github.com/saker-ai/filehub/pkg/storage"
	"github.com/saker-ai/filehub/pkg/store"
	"github.com/saker-ai/filehub/pkg/store/gormstore"
)

func TestPipelineEnqueueClaimsAssetOnce(t *testing.T) {
	cfg := config.Defaults()
	cfg.DSN = "sqlite://" + filepath.Join(t.TempDir(), "pipeline.db")
	cfg.Storage.Backend = config.BackendMemFS
	repo, err := gormstore.Open(t.Context(), cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	blobs, err := storage.New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	asset := &store.Asset{
		ID: "asset-once", TenantID: "default", Purpose: "general", Filename: "once.txt",
		ContentType: "text/plain", StorageKey: "default/general/asset-once/once.txt", Status: "uploaded",
	}
	if _, err := blobs.Put(t.Context(), asset.StorageKey, bytes.NewBufferString("payload")); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(t.Context(), asset); err != nil {
		t.Fatal(err)
	}
	pipeline := New(4, blobs, repo, nil)
	var processed atomic.Int64
	pipeline.ObserveProcessing(func(time.Duration) { processed.Add(1) })

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pipeline.Enqueue(t.Context(), asset)
		}()
	}
	wg.Wait()
	pipeline.Wait()
	got, err := repo.Get(t.Context(), "default", asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "ready" {
		t.Fatalf("asset status = %q, want ready", got.Status)
	}
	if got := processed.Load(); got != 1 {
		t.Fatalf("processing runs = %d, want 1", got)
	}
}

func TestOfficePreviewSupported(t *testing.T) {
	cases := []struct {
		filename    string
		contentType string
		want        bool
	}{
		{"slides.pptx", "application/zip", true},
		{"slides.ppt", "application/vnd.ms-powerpoint", true},
		{"legacy.doc", "application/zip", true},
		{"legacy.xls", "application/zip", true},
		{"deck.odp", "application/zip", true},
		{"report.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation", true},
		{"document.docx", "application/zip", false}, // rendered client-side
		{"sheet.xlsx", "application/zip", false},    // rendered client-side
		{"notes.txt", "text/plain", false},
		{"image.png", "image/png", false},
		{"", "application/msword", true}, // content-type fallback
	}
	for _, tc := range cases {
		asset := &store.Asset{Filename: tc.filename, ContentType: tc.contentType}
		if got := officePreviewSupported(asset); got != tc.want {
			t.Errorf("officePreviewSupported(%q, %q) = %v, want %v", tc.filename, tc.contentType, got, tc.want)
		}
	}
}
