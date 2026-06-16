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

func TestStoreAIReviews(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "assethub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	asset := testAsset("asset-reviewed", "reviewed.txt", "sha256:reviewed")
	if err := db.Create(ctx, asset); err != nil {
		t.Fatal(err)
	}
	score := 0.92
	confidence := 0.81
	review := &store.AIReview{
		ID:            "airev-a",
		TenantID:      "default",
		AssetID:       asset.ID,
		Model:         "reviewer-v1",
		Verdict:       "approved",
		Score:         &score,
		Summary:       "Meets prompt and quality bar.",
		Rubric:        "image-quality",
		Confidence:    &confidence,
		PromptVersion: "pv-1",
		ReviewJobID:   "job-1",
		RawResponseID: "resp-1",
		Metadata:      store.JSONMap{"dimension": "composition"},
	}
	if err := db.CreateAIReview(ctx, review); err != nil {
		t.Fatal(err)
	}

	items, err := db.ListAIReviews(ctx, "default", store.AIReviewFilter{AssetID: asset.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != review.ID || items[0].Score == nil || *items[0].Score != score || items[0].Rubric != "image-quality" || items[0].Confidence == nil || *items[0].Confidence != confidence {
		t.Fatalf("asset reviews = %#v, want stored review", items)
	}

	items, err = db.ListAIReviews(ctx, "default", store.AIReviewFilter{Model: "reviewer-v1", Verdict: "approved"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Summary != review.Summary {
		t.Fatalf("filtered reviews = %#v, want stored review", items)
	}

	if err := db.Delete(ctx, "default", asset.ID); err != nil {
		t.Fatal(err)
	}
	items, err = db.ListAIReviews(ctx, "default", store.AIReviewFilter{AssetID: asset.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("reviews after asset delete = %#v, want none", items)
	}
}

func TestStoreAssetReviews(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "assethub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	asset := testAsset("asset-human-review", "human.txt", "sha256:human")
	if err := db.Create(ctx, asset); err != nil {
		t.Fatal(err)
	}
	review := &store.AssetReview{
		ID:          "rev-a",
		TenantID:    "default",
		Title:       "Human batch",
		Status:      "open",
		ReferenceID: asset.ID,
		Reviewer:    "alice",
		Source:      "human",
		TraceID:     "trace-1",
		Metadata:    store.JSONMap{"queue": "qa"},
		Items: []store.AssetReviewItem{{
			ID:       "revi-a",
			AssetID:  asset.ID,
			Decision: "pending",
		}},
	}
	if err := db.CreateAssetReview(ctx, review); err != nil {
		t.Fatal(err)
	}

	loaded, err := db.GetAssetReview(ctx, "default", review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Title != review.Title || len(loaded.Items) != 1 || loaded.Items[0].AssetID != asset.ID {
		t.Fatalf("loaded review = %#v, want created review", loaded)
	}

	note := "best candidate"
	if err := db.UpdateAssetReviewItem(ctx, "default", review.ID, &store.AssetReviewItem{
		AssetID:  asset.ID,
		Decision: "best",
		Note:     note,
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err = db.GetAssetReview(ctx, "default", review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Items[0].Decision != "best" || loaded.Items[0].Note != note {
		t.Fatalf("updated review item = %#v, want best decision", loaded.Items[0])
	}

	loaded.Status = "completed"
	now := time.Now().UTC()
	loaded.CompletedAt = &now
	loaded.SelectedAssetID = asset.ID
	if err := db.UpdateAssetReview(ctx, loaded); err != nil {
		t.Fatal(err)
	}
	list, err := db.ListAssetReviews(ctx, "default", store.AssetReviewFilter{Status: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].SelectedAssetID != asset.ID {
		t.Fatalf("completed reviews = %#v, want selected asset", list)
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
