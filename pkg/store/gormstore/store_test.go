package gormstore

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/saker-ai/assethub/pkg/store"
)

func TestStoreCreateDedupeKeyUniqueOnlyWhenSet(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "assethub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	checksum := "sha256:duplicate"
	first := testAsset("asset-a", "a.txt", checksum)
	first.DedupeKey = &checksum
	if err := db.Create(ctx, first); err != nil {
		t.Fatal(err)
	}
	conflict := testAsset("asset-b", "b.txt", checksum)
	conflict.DedupeKey = &checksum
	if err := db.Create(ctx, conflict); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("conflict create err = %v, want ErrConflict", err)
	}

	allowA := testAsset("asset-c", "c.txt", checksum)
	allowB := testAsset("asset-d", "d.txt", checksum)
	if err := db.Create(ctx, allowA); err != nil {
		t.Fatalf("allow A create: %v", err)
	}
	if err := db.Create(ctx, allowB); err != nil {
		t.Fatalf("allow B create: %v", err)
	}
}

func TestStoreListFiltersByMetadata(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "assethub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	flux := testAsset("asset-flux", "flux.txt", "sha256:flux")
	flux.Metadata = store.JSONMap{"model": "flux", "reviewed": true, "workflow_id": "wf-1"}
	if err := db.Create(ctx, flux); err != nil {
		t.Fatal(err)
	}
	other := testAsset("asset-other", "other.txt", "sha256:other")
	other.Metadata = store.JSONMap{"model": "sdxl", "reviewed": false}
	if err := db.Create(ctx, other); err != nil {
		t.Fatal(err)
	}

	items, _, err := db.List(ctx, "default", store.AssetFilter{
		Metadata: []store.MetadataFilter{{Key: "workflow_id"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "asset-flux" {
		t.Fatalf("key metadata filter = %#v, want asset-flux", items)
	}

	items, _, err = db.List(ctx, "default", store.AssetFilter{
		Metadata: []store.MetadataFilter{
			{Key: "model", Value: "flux", HasValue: true},
			{Key: "reviewed", Value: true, HasValue: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "asset-flux" {
		t.Fatalf("exact metadata filters = %#v, want asset-flux", items)
	}

	flux.Metadata = store.JSONMap{"model": "sdxl", "reviewed": true}
	if err := db.Update(ctx, flux); err != nil {
		t.Fatal(err)
	}
	items, _, err = db.List(ctx, "default", store.AssetFilter{
		Metadata: []store.MetadataFilter{{Key: "workflow_id"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("stale metadata index result = %#v, want none", items)
	}
}

func TestStoreListCursorPagination(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "assethub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	base := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	for i, id := range []string{"asset-a", "asset-b", "asset-c"} {
		asset := testAsset(id, id+".txt", "sha256:"+id)
		asset.CreatedAt = base.Add(time.Duration(i) * time.Minute)
		if err := db.Create(ctx, asset); err != nil {
			t.Fatal(err)
		}
	}

	first, hasMore, err := db.List(ctx, "default", store.AssetFilter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !hasMore || len(first) != 1 || first[0].ID != "asset-c" {
		t.Fatalf("first page = %#v hasMore=%v, want asset-c and more", first, hasMore)
	}
	second, _, err := db.List(ctx, "default", store.AssetFilter{Limit: 1, Cursor: store.CursorFromAsset(first[0])})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].ID != "asset-b" {
		t.Fatalf("second page = %#v, want asset-b", second)
	}
}

func testAsset(id, filename, checksum string) *store.Asset {
	return &store.Asset{
		ID:          id,
		TenantID:    "default",
		Purpose:     "media",
		Filename:    filename,
		ContentType: "text/plain",
		Bytes:       1,
		StorageKey:  "default/media/2026-06/" + id + "/" + filename,
		Checksum:    checksum,
		Status:      "uploaded",
		Source:      "upload",
	}
}
