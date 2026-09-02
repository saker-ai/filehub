package api

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/saker-ai/filehub/pkg/config"
	filehubnotify "github.com/saker-ai/filehub/pkg/notify"
	"github.com/saker-ai/filehub/pkg/processing"
	"github.com/saker-ai/filehub/pkg/storage"
	"github.com/saker-ai/filehub/pkg/store/gormstore"
	"github.com/saker-ai/filehub/web"
	"github.com/saker-ai/saker-common/internaljwt"
)

func TestRouterAssetLifecycle(t *testing.T) {
	ts := newTestServer(t)

	uploadResp := ts.uploadAsset(t, "/v1/assets?on_duplicate=allow", "sample.png", pngBytes(t), map[string]string{
		"purpose":  "media",
		"tags":     "ai-generated,portrait",
		"metadata": `{"model":"flux","prompt":"test"}`,
		"source":   "ai-generated",
	})
	id := uploadResp["id"].(string)
	if !strings.HasPrefix(id, "asset-") {
		t.Fatalf("asset id = %q, want asset-*", id)
	}
	ts.waitReady(t, id)

	dup := ts.uploadAssetStatus(t, "/v1/assets", "sample.png", pngBytes(t), map[string]string{"purpose": "media"})
	if dup.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409; body=%s", dup.Code, dup.Body.String())
	}
	var dupBody map[string]map[string]any
	if err := json.Unmarshal(dup.Body.Bytes(), &dupBody); err != nil {
		t.Fatal(err)
	}
	if dupBody["error"]["asset_id"] != id || dupBody["error"]["request_id"] == "" {
		t.Fatalf("duplicate body = %#v, want asset_id and request_id", dupBody)
	}

	reuse := ts.uploadAsset(t, "/v1/assets?on_duplicate=reuse", "sample.png", pngBytes(t), map[string]string{"purpose": "media"})
	if reuse["id"] != id {
		t.Fatalf("reuse id = %v, want %s", reuse["id"], id)
	}

	list := ts.getJSON(t, "/v1/assets?tags=ai-generated,portrait&content_type=image/")
	data := list["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("filtered assets len = %d, want 1", len(data))
	}

	content := ts.request(t, http.MethodGet, "/v1/assets/"+id+"/content", nil, nil)
	if content.Code != http.StatusOK {
		t.Fatalf("content status = %d, body=%s", content.Code, content.Body.String())
	}
	etag := content.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag is empty")
	}
	notModified := ts.request(t, http.MethodGet, "/v1/assets/"+id+"/content", nil, map[string]string{"If-None-Match": etag})
	if notModified.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match status = %d, want 304", notModified.Code)
	}

	thumb := ts.request(t, http.MethodGet, "/v1/assets/"+id+"/thumbnail?width=32&height=32&format=jpg", nil, nil)
	if thumb.Code != http.StatusOK || thumb.Body.Len() == 0 {
		t.Fatalf("thumbnail status=%d len=%d body=%s", thumb.Code, thumb.Body.Len(), thumb.Body.String())
	}

	presignBody := bytes.NewBufferString(`{"expires_in":"5m"}`)
	presign := ts.request(t, http.MethodPost, "/v1/assets/"+id+"/presign", presignBody, map[string]string{"Content-Type": "application/json"})
	if presign.Code != http.StatusOK {
		t.Fatalf("presign status=%d body=%s", presign.Code, presign.Body.String())
	}
	var signed struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(presign.Body.Bytes(), &signed); err != nil {
		t.Fatal(err)
	}
	downloadPath := strings.TrimPrefix(signed.URL, "http://127.0.0.1:17040")
	signedResp := ts.request(t, http.MethodGet, downloadPath, nil, nil)
	if signedResp.Code != http.StatusOK {
		t.Fatalf("signed download status=%d body=%s", signedResp.Code, signedResp.Body.String())
	}

	patch := ts.request(t, http.MethodPatch, "/v1/assets/"+id, bytes.NewBufferString(`{"tags":["approved"],"metadata":{"reviewed":true}}`), map[string]string{"Content-Type": "application/json"})
	if patch.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", patch.Code, patch.Body.String())
	}

	stats := ts.getJSON(t, "/v1/assets/stats")
	if stats["total"].(float64) < 1 {
		t.Fatalf("stats total = %v, want >= 1", stats["total"])
	}
	byStatus := stats["by_status"].(map[string]any)
	if byStatus["ready"].(float64) < 1 {
		t.Fatalf("stats by_status.ready = %v, want >= 1", byStatus["ready"])
	}

	bulk := ts.request(t, http.MethodPost, "/v1/assets/bulk-delete", bytes.NewBufferString(`{"ids":["`+id+`"]}`), map[string]string{"Content-Type": "application/json"})
	if bulk.Code != http.StatusOK {
		t.Fatalf("bulk delete status=%d body=%s", bulk.Code, bulk.Body.String())
	}
	missing := ts.request(t, http.MethodGet, "/v1/assets/"+id, nil, nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("deleted get status = %d, want 404", missing.Code)
	}
}

func TestRouterExternalAssetAPICompatibility(t *testing.T) {
	ts := newTestServer(t)

	uploaded := ts.uploadAssetWithContentType(
		t,
		"/v1/external/assets",
		"compat.txt",
		"text/plain",
		[]byte("compatible asset"),
		nil,
	)
	id, _ := uploaded["id"].(string)
	if id == "" {
		t.Fatalf("upload response missing id: %#v", uploaded)
	}
	if uploaded["contentType"] != "text/plain" {
		t.Fatalf("contentType = %#v, want text/plain", uploaded["contentType"])
	}
	if uploaded["size"] != float64(len("compatible asset")) {
		t.Fatalf("size = %#v, want %d", uploaded["size"], len("compatible asset"))
	}
	signedURL, _ := uploaded["url"].(string)
	if signedURL == "" {
		t.Fatalf("upload response missing signed url: %#v", uploaded)
	}
	signedPath := strings.TrimPrefix(signedURL, "http://127.0.0.1:17040")
	signed := ts.request(t, http.MethodGet, signedPath, nil, nil)
	if signed.Code != http.StatusOK || signed.Body.String() != "compatible asset" {
		t.Fatalf("signed download status=%d body=%q", signed.Code, signed.Body.String())
	}
	expiresAt, _ := uploaded["expiresAt"].(float64)
	remaining := time.Until(time.Unix(int64(expiresAt), 0))
	if remaining < 7*24*time.Hour-5*time.Second || remaining > 7*24*time.Hour+5*time.Second {
		t.Fatalf("upload signed URL TTL = %s, want 7d", remaining)
	}

	content := ts.request(t, http.MethodGet, "/v1/external/assets/"+id, nil, nil)
	if content.Code != http.StatusOK || content.Body.String() != "compatible asset" {
		t.Fatalf("compat get status=%d body=%q", content.Code, content.Body.String())
	}
	if got := content.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("compat get Content-Type = %q", got)
	}

	head := ts.request(t, http.MethodHead, "/v1/external/assets/"+id, nil, nil)
	if head.Code != http.StatusOK {
		t.Fatalf("compat head status=%d body=%s", head.Code, head.Body.String())
	}
	missing := ts.request(t, http.MethodHead, "/v1/external/assets/missing", nil, nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("compat missing head status=%d, want 404", missing.Code)
	}

	presign := ts.request(t, http.MethodPost, "/v1/external/assets/"+id+"/presign", bytes.NewBufferString(`{"expiresIn":"5m"}`), map[string]string{"Content-Type": "application/json"})
	if presign.Code != http.StatusOK {
		t.Fatalf("compat presign status=%d body=%s", presign.Code, presign.Body.String())
	}
	var presigned struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(presign.Body.Bytes(), &presigned); err != nil {
		t.Fatalf("decode compat presign: %v", err)
	}
	if presigned.URL == "" {
		t.Fatal("compat presign returned empty url")
	}

	put := ts.request(t, http.MethodPut, "/v1/external/assets", bytes.NewBufferString("raw upload"), map[string]string{
		"Content-Type": "text/plain",
		"X-Filename":   "raw.txt",
	})
	if put.Code != http.StatusOK {
		t.Fatalf("compat put status=%d body=%s", put.Code, put.Body.String())
	}
	var putResult map[string]any
	if err := json.Unmarshal(put.Body.Bytes(), &putResult); err != nil {
		t.Fatalf("decode compat put: %v", err)
	}
	putID, _ := putResult["id"].(string)
	putContent := ts.request(t, http.MethodGet, "/v1/external/assets/"+putID, nil, nil)
	if putContent.Code != http.StatusOK || putContent.Body.String() != "raw upload" {
		t.Fatalf("compat put content status=%d body=%q", putContent.Code, putContent.Body.String())
	}
}

func TestRouterListAssetsFiltersByMetadata(t *testing.T) {
	ts := newTestServer(t)

	first := ts.uploadAsset(t, "/v1/assets?on_duplicate=allow", "first.txt", []byte("first"), map[string]string{
		"purpose":  "media",
		"metadata": `{"model":"flux","reviewed":true,"workflow_id":"wf-1"}`,
	})
	second := ts.uploadAsset(t, "/v1/assets?on_duplicate=allow", "second.txt", []byte("second"), map[string]string{
		"purpose":  "media",
		"metadata": `{"model":"sdxl","reviewed":false}`,
	})

	byKey := ts.getJSON(t, "/v1/assets?meta_key=workflow_id")
	byKeyData := byKey["data"].([]any)
	if len(byKeyData) != 1 || byKeyData[0].(map[string]any)["id"] != first["id"] {
		t.Fatalf("meta_key result = %#v, want first asset", byKeyData)
	}

	byValue := ts.getJSON(t, "/v1/assets?meta_key=model&meta_value=sdxl")
	byValueData := byValue["data"].([]any)
	if len(byValueData) != 1 || byValueData[0].(map[string]any)["id"] != second["id"] {
		t.Fatalf("meta_key/meta_value result = %#v, want second asset", byValueData)
	}

	byMap := ts.getJSON(t, "/v1/assets?metadata[model]=flux&metadata[reviewed]=true")
	byMapData := byMap["data"].([]any)
	if len(byMapData) != 1 || byMapData[0].(map[string]any)["id"] != first["id"] {
		t.Fatalf("metadata map result = %#v, want first asset", byMapData)
	}
}

func TestRouterAIReviewResults(t *testing.T) {
	ts := newTestServer(t)

	asset := ts.uploadAsset(t, "/v1/assets?on_duplicate=allow", "review-me.txt", []byte("review me"), map[string]string{
		"purpose": "media",
		"source":  "ai-generated",
	})
	id := asset["id"].(string)

	create := ts.request(t, http.MethodPost, "/v1/assets/"+id+"/ai-reviews", bytes.NewBufferString(`{
		"model":"reviewer-v1",
		"verdict":"approved",
		"score":0.87,
		"summary":"Useful result",
		"rubric":"image-quality",
		"confidence":0.91,
		"prompt_version":"pv-1",
		"review_job_id":"job-1",
		"raw_response_id":"resp-1",
		"metadata":{"reason":"matches prompt"}
	}`), map[string]string{"Content-Type": "application/json"})
	if create.Code != http.StatusOK {
		t.Fatalf("create ai review status=%d body=%s", create.Code, create.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created["asset_id"] != id || created["verdict"] != "approved" || created["model"] != "reviewer-v1" || created["rubric"] != "image-quality" || created["review_job_id"] != "job-1" {
		t.Fatalf("created review = %#v, want asset/model/verdict", created)
	}

	byAsset := ts.getJSON(t, "/v1/assets/"+id+"/ai-reviews")
	assetData := byAsset["data"].([]any)
	if len(assetData) != 1 || assetData[0].(map[string]any)["summary"] != "Useful result" {
		t.Fatalf("asset ai reviews = %#v, want created review", assetData)
	}

	global := ts.getJSON(t, "/v1/ai-reviews?asset_id="+id+"&verdict=approved")
	globalData := global["data"].([]any)
	if len(globalData) != 1 || globalData[0].(map[string]any)["asset_id"] != id {
		t.Fatalf("global ai reviews = %#v, want created review", globalData)
	}
}

func TestRouterHumanReviewWorkflow(t *testing.T) {
	ts := newTestServer(t)

	first := ts.uploadAsset(t, "/v1/assets?on_duplicate=allow", "first-review.txt", []byte("first"), map[string]string{"purpose": "media"})
	second := ts.uploadAsset(t, "/v1/assets?on_duplicate=allow", "second-review.txt", []byte("second"), map[string]string{"purpose": "media"})
	firstID := first["id"].(string)
	secondID := second["id"].(string)

	create := ts.request(t, http.MethodPost, "/v1/reviews", bytes.NewBufferString(`{
		"title":"Human pick",
		"reviewer":"alice",
		"reference_asset_id":"`+firstID+`",
		"asset_ids":["`+firstID+`","`+secondID+`"],
		"trace_id":"trace-1"
	}`), map[string]string{"Content-Type": "application/json"})
	if create.Code != http.StatusOK {
		t.Fatalf("create review status=%d body=%s", create.Code, create.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	reviewID := created["id"].(string)
	if created["status"] != "open" || len(created["items"].([]any)) != 2 {
		t.Fatalf("created review = %#v, want open review with two items", created)
	}

	updateItem := ts.request(t, http.MethodPatch, "/v1/reviews/"+reviewID+"/items/"+secondID, bytes.NewBufferString(`{"decision":"best","note":"best output"}`), map[string]string{"Content-Type": "application/json"})
	if updateItem.Code != http.StatusOK {
		t.Fatalf("update review item status=%d body=%s", updateItem.Code, updateItem.Body.String())
	}
	var itemUpdated map[string]any
	if err := json.Unmarshal(updateItem.Body.Bytes(), &itemUpdated); err != nil {
		t.Fatal(err)
	}
	items := itemUpdated["items"].([]any)
	foundBest := false
	for _, raw := range items {
		item := raw.(map[string]any)
		if item["asset_id"] == secondID && item["decision"] == "best" && item["note"] == "best output" {
			foundBest = true
		}
	}
	if !foundBest {
		t.Fatalf("updated review items = %#v, want best item", items)
	}

	complete := ts.request(t, http.MethodPatch, "/v1/reviews/"+reviewID, bytes.NewBufferString(`{"status":"completed","selected_asset_id":"`+secondID+`"}`), map[string]string{"Content-Type": "application/json"})
	if complete.Code != http.StatusOK {
		t.Fatalf("complete review status=%d body=%s", complete.Code, complete.Body.String())
	}
	completed := ts.getJSON(t, "/v1/reviews?status=completed")
	data := completed["data"].([]any)
	if len(data) != 1 || data[0].(map[string]any)["selected_asset_id"] != secondID {
		t.Fatalf("completed review list = %#v, want selected second asset", data)
	}
}

func TestRouterOpenAIFilesAndChunkUpload(t *testing.T) {
	ts := newTestServer(t)

	file := ts.uploadAsset(t, "/v1/files", "batch.jsonl", []byte(`{"custom_id":"1"}`+"\n"), map[string]string{"purpose": "batch"})
	fileID := file["id"].(string)
	if !strings.HasPrefix(fileID, "file-") || file["object"] != "file" {
		t.Fatalf("file response = %#v", file)
	}
	if file["content_type"] == "" {
		t.Fatalf("file content type missing: %#v", file)
	}
	content := ts.request(t, http.MethodGet, "/v1/files/"+fileID+"/content?download=true", nil, nil)
	if content.Code != http.StatusOK {
		t.Fatalf("file content status=%d body=%s", content.Code, content.Body.String())
	}
	if !strings.HasPrefix(content.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("content disposition = %q", content.Header().Get("Content-Disposition"))
	}

	create := ts.request(t, http.MethodPost, "/v1/uploads", bytes.NewBufferString(`{"filename":"large.txt","purpose":"media","content_type":"text/plain","total_bytes":11,"tags":["chunked"]}`), map[string]string{"Content-Type": "application/json"})
	if create.Code != http.StatusOK {
		t.Fatalf("create upload status=%d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		UploadID string `json:"upload_id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	part1 := ts.request(t, http.MethodPut, "/v1/uploads/"+created.UploadID+"/parts/1", bytes.NewBufferString("hello "), nil)
	if part1.Code != http.StatusOK {
		t.Fatalf("part1 status=%d body=%s", part1.Code, part1.Body.String())
	}
	part2 := ts.request(t, http.MethodPut, "/v1/uploads/"+created.UploadID+"/parts/2", bytes.NewBufferString("world"), nil)
	if part2.Code != http.StatusOK {
		t.Fatalf("part2 status=%d body=%s", part2.Code, part2.Body.String())
	}
	complete := ts.request(t, http.MethodPost, "/v1/uploads/"+created.UploadID+"/complete", bytes.NewBufferString(`{"parts":[{"part":1},{"part":2}]}`), map[string]string{"Content-Type": "application/json"})
	if complete.Code != http.StatusOK {
		t.Fatalf("complete status=%d body=%s", complete.Code, complete.Body.String())
	}
	var asset map[string]any
	if err := json.Unmarshal(complete.Body.Bytes(), &asset); err != nil {
		t.Fatal(err)
	}
	assetID := asset["id"].(string)
	ts.waitReady(t, assetID)
	body := ts.request(t, http.MethodGet, "/v1/assets/"+assetID+"/content", nil, nil)
	if got := body.Body.String(); got != "hello world" {
		t.Fatalf("chunk content = %q, want hello world", got)
	}
}

func TestRouterNativeS3MultipartUpload(t *testing.T) {
	s3fake := newFakeS3Server(t)
	cfg := config.Defaults()
	cfg.Storage.Backend = config.BackendS3
	cfg.Storage.S3Endpoint = s3fake.URL
	cfg.Storage.S3Bucket = "test-bucket"
	cfg.Storage.S3Region = "us-east-1"
	cfg.Storage.S3AccessKey = "test-key"
	cfg.Storage.S3SecretKey = "test-secret"
	cfg.APIKeyAuthEnabled = true
	cfg.APIKeys = []string{"test-key"}
	ts := newTestServerWithConfig(t, cfg)

	create := ts.request(t, http.MethodPost, "/v1/uploads", bytes.NewBufferString(`{"filename":"cloud.txt","purpose":"media","content_type":"text/plain","total_bytes":11}`), map[string]string{"Content-Type": "application/json"})
	if create.Code != http.StatusOK {
		t.Fatalf("create upload status=%d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		UploadID string `json:"upload_id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if s3fake.createCalls.Load() != 1 {
		t.Fatalf("native create multipart calls = %d, want 1", s3fake.createCalls.Load())
	}
	part1 := ts.request(t, http.MethodPut, "/v1/uploads/"+created.UploadID+"/parts/1", bytes.NewBufferString("hello "), nil)
	if part1.Code != http.StatusOK {
		t.Fatalf("part1 status=%d body=%s", part1.Code, part1.Body.String())
	}
	part2 := ts.request(t, http.MethodPut, "/v1/uploads/"+created.UploadID+"/parts/2", bytes.NewBufferString("world"), nil)
	if part2.Code != http.StatusOK {
		t.Fatalf("part2 status=%d body=%s", part2.Code, part2.Body.String())
	}
	if s3fake.uploadPartCalls.Load() != 2 {
		t.Fatalf("native upload part calls = %d, want 2", s3fake.uploadPartCalls.Load())
	}
	complete := ts.request(t, http.MethodPost, "/v1/uploads/"+created.UploadID+"/complete", bytes.NewBufferString(`{"parts":[{"part":1},{"part":2}]}`), map[string]string{"Content-Type": "application/json"})
	if complete.Code != http.StatusOK {
		t.Fatalf("complete status=%d body=%s", complete.Code, complete.Body.String())
	}
	if s3fake.completeCalls.Load() != 1 {
		t.Fatalf("native complete multipart calls = %d, want 1", s3fake.completeCalls.Load())
	}
	var asset map[string]any
	if err := json.Unmarshal(complete.Body.Bytes(), &asset); err != nil {
		t.Fatal(err)
	}
	assetID := asset["id"].(string)
	ts.waitReady(t, assetID)
	body := ts.request(t, http.MethodGet, "/v1/assets/"+assetID+"/content", nil, nil)
	if got := body.Body.String(); got != "hello world" {
		t.Fatalf("native multipart content = %q, want hello world", got)
	}
	presign := ts.request(t, http.MethodPost, "/v1/assets/"+assetID+"/presign", bytes.NewBufferString(`{"expires_in":"5m"}`), map[string]string{"Content-Type": "application/json"})
	if presign.Code != http.StatusOK {
		t.Fatalf("presign status=%d body=%s", presign.Code, presign.Body.String())
	}
	var signed struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(presign.Body.Bytes(), &signed); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(signed.URL, s3fake.URL+"/test-bucket/") || strings.Contains(signed.URL, "/v1/dl/") {
		t.Fatalf("presign URL = %q, want native S3 URL", signed.URL)
	}
	resp, err := http.Get(signed.URL)
	if err != nil {
		t.Fatalf("get native presign URL: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("native presign GET status = %d", resp.StatusCode)
	}
}

func TestRouterNativeS3ExternalUploadUsesPublicEndpointAndDefaultTTL(t *testing.T) {
	s3fake := newFakeS3Server(t)
	cfg := config.Defaults()
	cfg.Storage.Backend = config.BackendS3
	cfg.Storage.S3Endpoint = s3fake.URL
	cfg.Storage.S3PublicEndpoint = "https://assets.example.com"
	cfg.Storage.S3Bucket = "test-bucket"
	cfg.Storage.S3Region = "us-east-1"
	cfg.Storage.S3AccessKey = "test-key"
	cfg.Storage.S3SecretKey = "test-secret"
	cfg.APIKeyAuthEnabled = true
	cfg.APIKeys = []string{"test-key"}
	ts := newTestServerWithConfig(t, cfg)

	uploaded := ts.uploadAsset(t, "/v1/external/assets", "public.txt", []byte("public asset"), nil)
	rawURL, _ := uploaded["url"].(string)
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse presign URL: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "assets.example.com" {
		t.Fatalf("presign URL = %q, want public endpoint", rawURL)
	}
	if got := parsed.Query().Get("X-Amz-Expires"); got != "604800" {
		t.Fatalf("X-Amz-Expires = %q, want 604800", got)
	}
	expiresAt, _ := uploaded["expiresAt"].(float64)
	remaining := time.Until(time.Unix(int64(expiresAt), 0))
	if remaining < 7*24*time.Hour-5*time.Second || remaining > 7*24*time.Hour+5*time.Second {
		t.Fatalf("presign TTL = %s, want 7d", remaining)
	}

	assetID, _ := uploaded["id"].(string)
	tests := []struct {
		name        string
		path        string
		body        io.Reader
		wantExpires string
		wantTTL     time.Duration
	}{
		{
			name:        "asset API default TTL",
			path:        "/v1/assets/" + assetID + "/presign",
			wantExpires: "604800",
			wantTTL:     7 * 24 * time.Hour,
		},
		{
			name:        "external API explicit TTL",
			path:        "/v1/external/assets/" + assetID + "/presign",
			body:        bytes.NewBufferString(`{"expiresIn":"1h"}`),
			wantExpires: "3600",
			wantTTL:     time.Hour,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := ts.request(t, http.MethodPost, tt.path, tt.body, map[string]string{"Content-Type": "application/json"})
			if response.Code != http.StatusOK {
				t.Fatalf("presign status=%d body=%s", response.Code, response.Body.String())
			}
			var signed struct {
				URL       string `json:"url"`
				ExpiresAt int64  `json:"expires_at"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &signed); err != nil {
				t.Fatal(err)
			}
			parsed, err := url.Parse(signed.URL)
			if err != nil {
				t.Fatalf("parse presign URL: %v", err)
			}
			if parsed.Scheme != "https" || parsed.Host != "assets.example.com" {
				t.Fatalf("presign URL = %q, want public endpoint", signed.URL)
			}
			if got := parsed.Query().Get("X-Amz-Expires"); got != tt.wantExpires {
				t.Fatalf("X-Amz-Expires = %q, want %s", got, tt.wantExpires)
			}
			remaining := time.Until(time.Unix(signed.ExpiresAt, 0))
			if remaining < tt.wantTTL-5*time.Second || remaining > tt.wantTTL+5*time.Second {
				t.Fatalf("presign TTL = %s, want %s", remaining, tt.wantTTL)
			}
		})
	}
}

func TestRouterDirectS3Upload(t *testing.T) {
	s3fake := newFakeS3Server(t)
	cfg := config.Defaults()
	cfg.Storage.Backend = config.BackendS3
	cfg.Storage.S3Endpoint = s3fake.URL
	cfg.Storage.S3Bucket = "test-bucket"
	cfg.Storage.S3Region = "us-east-1"
	cfg.Storage.S3AccessKey = "test-key"
	cfg.Storage.S3SecretKey = "test-secret"
	cfg.APIKeyAuthEnabled = true
	cfg.APIKeys = []string{"test-key"}
	ts := newTestServerWithConfig(t, cfg)

	create := ts.request(t, http.MethodPost, "/v1/uploads", bytes.NewBufferString(`{"mode":"direct","filename":"direct.txt","purpose":"media","content_type":"text/plain","total_bytes":11}`), map[string]string{"Content-Type": "application/json"})
	if create.Code != http.StatusOK {
		t.Fatalf("create direct upload status=%d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		UploadID string            `json:"upload_id"`
		AssetID  string            `json:"asset_id"`
		URL      string            `json:"url"`
		Headers  map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.UploadID == "" || created.AssetID == "" || created.URL == "" {
		t.Fatalf("direct upload create response missing fields: %#v", created)
	}
	session, err := ts.db.GetSession(t.Context(), "default", created.UploadID)
	if err != nil || !strings.Contains(session.StorageKey, "/_uploads/") {
		t.Fatalf("direct upload session must use staging: session=%#v err=%v", session, err)
	}
	putReq, err := http.NewRequest(http.MethodPut, created.URL, bytes.NewBufferString("hello world"))
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range created.Headers {
		putReq.Header.Set(key, value)
	}
	if putReq.Header.Get("Content-Type") == "" {
		putReq.Header.Set("Content-Type", "text/plain")
	}
	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = putResp.Body.Close()
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("direct put status=%d", putResp.StatusCode)
	}
	complete := ts.request(t, http.MethodPost, "/v1/uploads/"+created.UploadID+"/complete", bytes.NewBufferString(`{}`), map[string]string{"Content-Type": "application/json"})
	if complete.Code != http.StatusOK {
		t.Fatalf("complete direct status=%d body=%s", complete.Code, complete.Body.String())
	}
	var asset map[string]any
	if err := json.Unmarshal(complete.Body.Bytes(), &asset); err != nil {
		t.Fatal(err)
	}
	if asset["id"] != created.AssetID || asset["bytes"].(float64) != 11 {
		t.Fatalf("direct asset = %#v, want id %s and 11 bytes", asset, created.AssetID)
	}
	ts.waitReady(t, created.AssetID)
	body := ts.request(t, http.MethodGet, "/v1/assets/"+created.AssetID+"/content", nil, nil)
	if got := body.Body.String(); got != "hello world" {
		t.Fatalf("direct content = %q, want hello world", got)
	}
	overwriteReq, err := http.NewRequest(http.MethodPut, created.URL, bytes.NewBufferString("overwritten!"))
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range created.Headers {
		overwriteReq.Header.Set(key, value)
	}
	overwriteResp, err := http.DefaultClient.Do(overwriteReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = overwriteResp.Body.Close()
	body = ts.request(t, http.MethodGet, "/v1/assets/"+created.AssetID+"/content", nil, nil)
	if got := body.Body.String(); got != "hello world" {
		t.Fatalf("completed asset was changed through stale upload URL: %q", got)
	}
}

func TestRouterExternalDirectObjectStoreUpload(t *testing.T) {
	for _, backend := range []string{config.BackendS3, config.BackendOSS} {
		t.Run(backend, func(t *testing.T) {
			s3fake := newFakeS3Server(t)
			cfg := config.Defaults()
			cfg.Storage.Backend = backend
			cfg.Storage.S3Endpoint = s3fake.URL
			cfg.Storage.S3Bucket = "test-bucket"
			cfg.Storage.S3Region = "us-east-1"
			cfg.Storage.S3AccessKey = "test-key"
			cfg.Storage.S3SecretKey = "test-secret"
			cfg.APIKeyAuthEnabled = true
			cfg.APIKeys = []string{"test-key"}
			ts := newTestServerWithConfig(t, cfg)
			capabilities := ts.request(t, http.MethodGet, "/v1/external/capabilities", nil, nil)
			if capabilities.Code != http.StatusOK || !strings.Contains(capabilities.Body.String(), `"directMultipartUpload":true`) {
				t.Fatalf("external capabilities status=%d body=%s", capabilities.Code, capabilities.Body.String())
			}

			create := ts.request(t, http.MethodPost, "/v1/external/uploads", bytes.NewBufferString(
				`{"mode":"direct","filename":"direct.txt","purpose":"general","contentType":"text/plain","totalBytes":11}`,
			), map[string]string{"Content-Type": "application/json"})
			if create.Code != http.StatusOK {
				t.Fatalf("create external direct upload status=%d body=%s", create.Code, create.Body.String())
			}
			var created struct {
				UploadID string            `json:"uploadId"`
				AssetID  string            `json:"assetId"`
				Method   string            `json:"method"`
				URL      string            `json:"url"`
				Headers  map[string]string `json:"headers"`
			}
			if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			if created.UploadID == "" || created.AssetID == "" || created.Method != http.MethodPut || created.URL == "" {
				t.Fatalf("external direct upload response = %#v", created)
			}

			putReq, err := http.NewRequest(created.Method, created.URL, bytes.NewBufferString("hello world"))
			if err != nil {
				t.Fatal(err)
			}
			for key, value := range created.Headers {
				putReq.Header.Set(key, value)
			}
			putResp, err := http.DefaultClient.Do(putReq)
			if err != nil {
				t.Fatal(err)
			}
			_ = putResp.Body.Close()
			if putResp.StatusCode != http.StatusOK {
				t.Fatalf("provider put status=%d", putResp.StatusCode)
			}

			missingChecksum := ts.request(t, http.MethodPost, "/v1/external/uploads/"+created.UploadID+"/complete", bytes.NewBufferString(`{}`), map[string]string{"Content-Type": "application/json"})
			if missingChecksum.Code != http.StatusBadRequest || !strings.Contains(missingChecksum.Body.String(), "checksum_required") {
				t.Fatalf("missing checksum status=%d body=%s", missingChecksum.Code, missingChecksum.Body.String())
			}
			badComplete := ts.request(t, http.MethodPost, "/v1/external/uploads/"+created.UploadID+"/complete", bytes.NewBufferString(`{"checksum":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), map[string]string{"Content-Type": "application/json"})
			if badComplete.Code != http.StatusBadRequest || !strings.Contains(badComplete.Body.String(), "checksum_mismatch") {
				t.Fatalf("checksum mismatch status=%d body=%s", badComplete.Code, badComplete.Body.String())
			}
			digest := sha256.Sum256([]byte("hello world"))
			completeBody := fmt.Sprintf(`{"checksum":"sha256:%s"}`, hex.EncodeToString(digest[:]))
			complete := ts.request(t, http.MethodPost, "/v1/external/uploads/"+created.UploadID+"/complete", bytes.NewBufferString(completeBody), map[string]string{"Content-Type": "application/json"})
			if complete.Code != http.StatusOK {
				t.Fatalf("complete external direct upload status=%d body=%s", complete.Code, complete.Body.String())
			}
			var asset struct {
				ID          string `json:"id"`
				ContentType string `json:"contentType"`
				Size        int64  `json:"size"`
			}
			if err := json.Unmarshal(complete.Body.Bytes(), &asset); err != nil {
				t.Fatal(err)
			}
			if asset.ID != created.AssetID || asset.ContentType != "text/plain" || asset.Size != 11 {
				t.Fatalf("completed external asset = %#v", asset)
			}
			repeated := ts.request(t, http.MethodPost, "/v1/external/uploads/"+created.UploadID+"/complete", bytes.NewBufferString(completeBody), map[string]string{"Content-Type": "application/json"})
			if repeated.Code != http.StatusOK {
				t.Fatalf("idempotent completion status=%d body=%s", repeated.Code, repeated.Body.String())
			}
			var repeatedAsset struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(repeated.Body.Bytes(), &repeatedAsset); err != nil || repeatedAsset.ID != asset.ID {
				t.Fatalf("idempotent completion asset=%#v err=%v", repeatedAsset, err)
			}
			ts.waitReady(t, asset.ID)
			content := ts.request(t, http.MethodGet, "/v1/external/assets/"+asset.ID, nil, nil)
			if content.Code != http.StatusOK || content.Body.String() != "hello world" {
				t.Fatalf("external content status=%d body=%q", content.Code, content.Body.String())
			}
			cancel := ts.request(t, http.MethodDelete, "/v1/external/uploads/"+created.UploadID, nil, nil)
			if cancel.Code != http.StatusConflict {
				t.Fatalf("cancel completed upload status=%d body=%s", cancel.Code, cancel.Body.String())
			}
			content = ts.request(t, http.MethodGet, "/v1/external/assets/"+asset.ID, nil, nil)
			if content.Code != http.StatusOK || content.Body.String() != "hello world" {
				t.Fatalf("content after rejected cancellation status=%d body=%q", content.Code, content.Body.String())
			}
		})
	}
}

func TestRouterExternalDirectUploadRejectsConflictingNaming(t *testing.T) {
	s3fake := newFakeS3Server(t)
	cfg := config.Defaults()
	cfg.Storage.Backend = config.BackendOSS
	cfg.Storage.S3Endpoint = s3fake.URL
	cfg.Storage.S3Bucket = "test-bucket"
	cfg.Storage.S3Region = "us-east-1"
	cfg.Storage.S3AccessKey = "test-key"
	cfg.Storage.S3SecretKey = "test-secret"
	cfg.APIKeyAuthEnabled = true
	cfg.APIKeys = []string{"test-key"}
	ts := newTestServerWithConfig(t, cfg)

	create := ts.request(t, http.MethodPost, "/v1/external/uploads", bytes.NewBufferString(
		`{"mode":"direct","filename":"direct.txt","purpose":"general","contentType":"text/plain","content_type":"image/png"}`,
	), map[string]string{"Content-Type": "application/json"})
	if create.Code != http.StatusBadRequest {
		t.Fatalf("conflicting naming status=%d body=%s", create.Code, create.Body.String())
	}
	create = ts.request(t, http.MethodPost, "/v1/external/uploads", bytes.NewBufferString(
		`{"filename":"direct.txt","purpose":"general","contentType":"text/plain"}`,
	), map[string]string{"Content-Type": "application/json"})
	if create.Code != http.StatusBadRequest {
		t.Fatalf("missing direct mode status=%d body=%s", create.Code, create.Body.String())
	}
}

func TestRouterDirectS3MultipartUpload(t *testing.T) {
	s3fake := newFakeS3Server(t)
	cfg := config.Defaults()
	cfg.Storage.Backend = config.BackendS3
	cfg.Storage.S3Endpoint = s3fake.URL
	cfg.Storage.S3Bucket = "test-bucket"
	cfg.Storage.S3Region = "us-east-1"
	cfg.Storage.S3AccessKey = "test-key"
	cfg.Storage.S3SecretKey = "test-secret"
	cfg.APIKeyAuthEnabled = true
	cfg.APIKeys = []string{"test-key"}
	ts := newTestServerWithConfig(t, cfg)

	create := ts.request(t, http.MethodPost, "/v1/uploads", bytes.NewBufferString(`{"mode":"direct_multipart","filename":"direct-large.txt","purpose":"media","content_type":"text/plain","total_bytes":11}`), map[string]string{"Content-Type": "application/json"})
	if create.Code != http.StatusOK {
		t.Fatalf("create direct multipart status=%d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		UploadID string `json:"upload_id"`
		AssetID  string `json:"asset_id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if s3fake.createCalls.Load() != 1 {
		t.Fatalf("direct multipart create calls = %d, want 1", s3fake.createCalls.Load())
	}
	etags := make([]string, 0, 2)
	for i, data := range []string{"hello ", "world"} {
		partNum := i + 1
		presign := ts.request(t, http.MethodPost, "/v1/uploads/"+created.UploadID+"/parts/"+strconv.Itoa(partNum)+"/presign", nil, nil)
		if presign.Code != http.StatusOK {
			t.Fatalf("presign part %d status=%d body=%s", partNum, presign.Code, presign.Body.String())
		}
		var signed struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		}
		if err := json.Unmarshal(presign.Body.Bytes(), &signed); err != nil {
			t.Fatal(err)
		}
		putReq, err := http.NewRequest(http.MethodPut, signed.URL, bytes.NewBufferString(data))
		if err != nil {
			t.Fatal(err)
		}
		for key, value := range signed.Headers {
			putReq.Header.Set(key, value)
		}
		putResp, err := http.DefaultClient.Do(putReq)
		if err != nil {
			t.Fatal(err)
		}
		_ = putResp.Body.Close()
		if putResp.StatusCode != http.StatusOK {
			t.Fatalf("direct part %d put status=%d", partNum, putResp.StatusCode)
		}
		etags = append(etags, putResp.Header.Get("ETag"))
	}
	completeBody := fmt.Sprintf(`{"parts":[{"part":1,"etag":%q},{"part":2,"etag":%q}]}`, etags[0], etags[1])
	complete := ts.request(t, http.MethodPost, "/v1/uploads/"+created.UploadID+"/complete", bytes.NewBufferString(completeBody), map[string]string{"Content-Type": "application/json"})
	if complete.Code != http.StatusOK {
		t.Fatalf("complete direct multipart status=%d body=%s", complete.Code, complete.Body.String())
	}
	if s3fake.uploadPartCalls.Load() != 2 || s3fake.completeCalls.Load() != 1 {
		t.Fatalf("direct multipart calls upload=%d complete=%d, want 2/1", s3fake.uploadPartCalls.Load(), s3fake.completeCalls.Load())
	}
	ts.waitReady(t, created.AssetID)
	body := ts.request(t, http.MethodGet, "/v1/assets/"+created.AssetID+"/content", nil, nil)
	if got := body.Body.String(); got != "hello world" {
		t.Fatalf("direct multipart content = %q, want hello world", got)
	}

	externalCreate := ts.request(t, http.MethodPost, "/v1/external/uploads", bytes.NewBufferString(`{"mode":"direct_multipart","filename":"external-large.txt","purpose":"media","contentType":"text/plain","totalBytes":5}`), map[string]string{"Content-Type": "application/json"})
	if externalCreate.Code != http.StatusOK {
		t.Fatalf("create external multipart status=%d body=%s", externalCreate.Code, externalCreate.Body.String())
	}
	var externalSession struct {
		UploadID string `json:"uploadId"`
	}
	if err := json.Unmarshal(externalCreate.Body.Bytes(), &externalSession); err != nil || externalSession.UploadID == "" {
		t.Fatalf("external multipart session=%#v err=%v", externalSession, err)
	}
	externalPresign := ts.request(t, http.MethodPost, "/v1/external/uploads/"+externalSession.UploadID+"/parts/1/presign", nil, nil)
	if externalPresign.Code != http.StatusOK || !strings.Contains(externalPresign.Body.String(), `"uploadId"`) || !strings.Contains(externalPresign.Body.String(), `"expiresAt"`) {
		t.Fatalf("external multipart presign status=%d body=%s", externalPresign.Code, externalPresign.Body.String())
	}
	cancel := ts.request(t, http.MethodDelete, "/v1/external/uploads/"+externalSession.UploadID, nil, nil)
	if cancel.Code != http.StatusOK {
		t.Fatalf("cancel external multipart status=%d body=%s", cancel.Code, cancel.Body.String())
	}
}

func TestRouterExternalURLAsyncSuccess(t *testing.T) {
	ts := newTestServer(t)
	withExternalTestHooks(t)

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("external asset"))
	}))
	t.Cleanup(source.Close)

	create := ts.request(t, http.MethodPost, "/v1/assets", bytes.NewBufferString(`{"url":"`+source.URL+`/file.txt","purpose":"media","tags":["external"],"metadata":{"model":"remote"}}`), map[string]string{"Content-Type": "application/json"})
	if create.Code != http.StatusOK {
		t.Fatalf("create external status=%d body=%s", create.Code, create.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	id := created["id"].(string)
	if created["status"] != "processing" {
		t.Fatalf("initial external status = %v, want processing", created["status"])
	}
	ts.waitReady(t, id)
	body := ts.request(t, http.MethodGet, "/v1/assets/"+id+"/content", nil, nil)
	if got := body.Body.String(); got != "external asset" {
		t.Fatalf("external content = %q, want external asset", got)
	}
	list := ts.getJSON(t, "/v1/assets?source=external-url&tags=external")
	if got := len(list["data"].([]any)); got != 1 {
		t.Fatalf("external list len = %d, want 1", got)
	}
}

func TestRouterExternalURLAsyncRetryFailure(t *testing.T) {
	ts := newTestServer(t)
	withExternalTestHooks(t)

	var attempts atomic.Int64
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "not yet", http.StatusBadGateway)
	}))
	t.Cleanup(source.Close)

	create := ts.request(t, http.MethodPost, "/v1/assets", bytes.NewBufferString(`{"url":"`+source.URL+`/failed.txt","purpose":"media"}`), map[string]string{"Content-Type": "application/json"})
	if create.Code != http.StatusOK {
		t.Fatalf("create external status=%d body=%s", create.Code, create.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	id := created["id"].(string)
	ts.waitStatus(t, id, "error")
	if got := attempts.Load(); got != 3 {
		t.Fatalf("external fetch attempts = %d, want 3", got)
	}
}

func TestRouterMediaMetadataExtraction(t *testing.T) {
	ts := newTestServer(t)

	model := ts.uploadAssetWithContentType(t, "/v1/assets?on_duplicate=allow", "triangle.obj", "model/obj", []byte(strings.Join([]string{
		"v 0 0 0",
		"v 1 0 0",
		"v 0 1 0",
		"usemtl matte",
		"f 1 2 3",
		"",
	}, "\n")), map[string]string{"purpose": "media"})
	modelID := model["id"].(string)
	ts.waitReady(t, modelID)
	modelAsset := ts.getJSON(t, "/v1/assets/"+modelID)
	modelMeta := modelAsset["metadata"].(map[string]any)
	if modelMeta["model.vertices"].(float64) != 3 || modelMeta["model.faces"].(float64) != 1 || modelMeta["model.materials"].(float64) != 1 {
		t.Fatalf("model metadata = %#v", modelMeta)
	}

	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not available")
	}
	audio := ts.uploadAssetWithContentType(t, "/v1/assets?on_duplicate=allow", "tone.wav", "audio/wav", wavBytes(), map[string]string{"purpose": "media"})
	audioID := audio["id"].(string)
	ts.waitReady(t, audioID)
	audioAsset := ts.getJSON(t, "/v1/assets/"+audioID)
	audioMeta := audioAsset["metadata"].(map[string]any)
	if audioMeta["media.sample_rate"].(float64) != 8000 || audioMeta["media.channels"].(float64) != 1 {
		t.Fatalf("audio metadata = %#v", audioMeta)
	}
	if _, ok := audioMeta["media.duration"]; !ok {
		t.Fatalf("audio metadata missing duration: %#v", audioMeta)
	}
}

func TestRouterVideoThumbnailAndMetadata(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not available")
	}
	videoPath := filepath.Join(t.TempDir(), "sample.mp4")
	cmd := exec.Command(
		"ffmpeg",
		"-v", "error",
		"-y",
		"-f", "lavfi",
		"-i", "color=c=red:s=32x32:d=1",
		"-pix_fmt", "yuv420p",
		videoPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg test video generation failed: %v: %s", err, string(out))
	}
	data, err := os.ReadFile(videoPath)
	if err != nil {
		t.Fatal(err)
	}

	ts := newTestServer(t)
	video := ts.uploadAssetWithContentType(t, "/v1/assets?on_duplicate=allow", "sample.mp4", "video/mp4", data, map[string]string{"purpose": "media"})
	videoID := video["id"].(string)
	ts.waitReady(t, videoID)
	videoAsset := ts.getJSON(t, "/v1/assets/"+videoID)
	meta := videoAsset["metadata"].(map[string]any)
	if meta["media.width"].(float64) != 32 || meta["media.height"].(float64) != 32 {
		t.Fatalf("video metadata = %#v", meta)
	}
	if _, ok := meta["media.duration"]; !ok {
		t.Fatalf("video metadata missing duration: %#v", meta)
	}
	thumb := ts.request(t, http.MethodGet, "/v1/assets/"+videoID+"/thumbnail?width=32&height=32&format=jpg", nil, nil)
	if thumb.Code != http.StatusOK || thumb.Body.Len() == 0 {
		t.Fatalf("video thumbnail status=%d len=%d body=%s", thumb.Code, thumb.Body.Len(), thumb.Body.String())
	}
}

func TestRouterStorageQuota(t *testing.T) {
	cfg := config.Defaults()
	cfg.MaxStorageBytes = 10
	cfg.APIKeyAuthEnabled = true
	cfg.APIKeys = []string{"test-key"}
	ts := newTestServerWithConfig(t, cfg)

	ok := ts.uploadAssetStatus(t, "/v1/assets?on_duplicate=allow", "small.txt", []byte("123456"), map[string]string{"purpose": "media"})
	if ok.Code != http.StatusOK {
		t.Fatalf("small upload status=%d body=%s", ok.Code, ok.Body.String())
	}
	tooLarge := ts.uploadAssetStatus(t, "/v1/assets?on_duplicate=allow", "large.txt", []byte("12345"), map[string]string{"purpose": "media"})
	if tooLarge.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("quota status=%d, want 413; body=%s", tooLarge.Code, tooLarge.Body.String())
	}
	if !strings.Contains(tooLarge.Body.String(), "storage_quota_exceeded") {
		t.Fatalf("quota body missing code: %s", tooLarge.Body.String())
	}
}

func TestRouterChunkCompleteStorageQuota(t *testing.T) {
	cfg := config.Defaults()
	cfg.MaxStorageBytes = 10
	cfg.APIKeyAuthEnabled = true
	cfg.APIKeys = []string{"test-key"}
	ts := newTestServerWithConfig(t, cfg)

	create := ts.request(t, http.MethodPost, "/v1/uploads", bytes.NewBufferString(`{"filename":"large.txt","purpose":"media","content_type":"text/plain","total_bytes":11}`), map[string]string{"Content-Type": "application/json"})
	if create.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("create quota status=%d, want 413; body=%s", create.Code, create.Body.String())
	}

	create = ts.request(t, http.MethodPost, "/v1/uploads", bytes.NewBufferString(`{"filename":"unknown.txt","purpose":"media","content_type":"text/plain"}`), map[string]string{"Content-Type": "application/json"})
	if create.Code != http.StatusOK {
		t.Fatalf("create upload status=%d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		UploadID string `json:"upload_id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	part := ts.request(t, http.MethodPut, "/v1/uploads/"+created.UploadID+"/parts/1", bytes.NewBufferString("12345"), nil)
	if part.Code != http.StatusOK {
		t.Fatalf("part status=%d body=%s", part.Code, part.Body.String())
	}
	ok := ts.uploadAssetStatus(t, "/v1/assets?on_duplicate=allow", "small.txt", []byte("123456"), map[string]string{"purpose": "media"})
	if ok.Code != http.StatusOK {
		t.Fatalf("small upload status=%d body=%s", ok.Code, ok.Body.String())
	}
	complete := ts.request(t, http.MethodPost, "/v1/uploads/"+created.UploadID+"/complete", bytes.NewBufferString(`{"parts":[{"part":1}]}`), map[string]string{"Content-Type": "application/json"})
	if complete.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("complete quota status=%d, want 413; body=%s", complete.Code, complete.Body.String())
	}
}

func TestRouterAuth(t *testing.T) {
	ts := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/assets", nil)
	req.Header.Set("X-Request-ID", "req-test")
	rec := httptest.NewRecorder()
	ts.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"request_id":"req-test"`) {
		t.Fatalf("error body missing request ID: %s", rec.Body.String())
	}
}

func TestRouterAPIKeyAuthDisabledByDefault(t *testing.T) {
	cfg := config.Defaults()
	if cfg.APIKeyAuthEnabled {
		t.Fatal("API key auth enabled by default")
	}
	cfg.APIKeys = []string{"test-key"}
	ts := newTestServerWithConfig(t, cfg)

	rec := ts.request(t, http.MethodGet, "/v1/assets", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "invalid_api_key") {
		t.Fatalf("API key path was used while disabled: %s", rec.Body.String())
	}
}

func TestRouterAPIKeyAuthRequiresExplicitEnable(t *testing.T) {
	cfg := config.Defaults()
	cfg.APIKeyAuthEnabled = true
	cfg.APIKeys = []string{"test-key"}
	ts := newTestServerWithConfig(t, cfg)

	rec := ts.request(t, http.MethodGet, "/v1/assets", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRouterInternalJWTAuth(t *testing.T) {
	cfg := config.Defaults()
	cfg.APIKeys = nil
	cfg.InternalAuth.Enabled = true
	cfg.InternalAuth.MasterSecret = "0123456789abcdef0123456789abcdef"
	ts := newTestServerWithConfig(t, cfg)

	signer, err := internaljwt.NewSigner("synapse", cfg.InternalAuth.MasterSecret, cfg.InternalAuth.TTL)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	token, _, err := signer.Sign(internaljwt.SignInput{
		Audience:      "filehub",
		TenantID:      "tenant-jwt",
		PrincipalType: "user",
		PrincipalID:   "user-jwt",
		Scopes:        []string{"filehub:read", "filehub:upload", "filehub:write"},
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("purpose", "media"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "sample.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(pngBytes(t)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	rec := ts.request(t, http.MethodPost, "/v1/assets?on_duplicate=allow", &body, map[string]string{
		"Content-Type":                          writer.FormDataContentType(),
		internaljwt.HeaderInternalAuthorization: "Bearer " + token,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	id := out["id"].(string)
	asset, err := ts.db.Get(context.Background(), "tenant-jwt", id)
	if err != nil {
		t.Fatalf("asset by jwt tenant: %v", err)
	}
	if asset.TenantID != "tenant-jwt" {
		t.Fatalf("tenant = %q", asset.TenantID)
	}
}

func TestRouterOpenAPIAndMetrics(t *testing.T) {
	ts := newTestServer(t)
	asset := ts.uploadAsset(t, "/v1/assets?on_duplicate=allow", "sample.png", pngBytes(t), map[string]string{"purpose": "media"})
	assetID := asset["id"].(string)
	ts.waitReady(t, assetID)
	for range 2 {
		thumb := ts.request(t, http.MethodGet, "/v1/assets/"+assetID+"/thumbnail?width=32&height=32&format=jpg", nil, nil)
		if thumb.Code != http.StatusOK {
			t.Fatalf("thumbnail status=%d body=%s", thumb.Code, thumb.Body.String())
		}
	}

	openapi := ts.request(t, http.MethodGet, "/openapi.json", nil, nil)
	if openapi.Code != http.StatusOK {
		t.Fatalf("openapi status=%d body=%s", openapi.Code, openapi.Body.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(openapi.Body.Bytes(), &doc); err != nil {
		t.Fatalf("openapi json: %v", err)
	}
	if doc["openapi"] != "3.1.0" {
		t.Fatalf("openapi version = %v, want 3.1.0", doc["openapi"])
	}
	paths := doc["paths"].(map[string]any)
	for _, path := range []string{"/v1/files", "/v1/assets", "/v1/assets/{id}/presign", "/v1/external/capabilities", "/v1/external/uploads/{id}/parts/{part}/presign", "/v1/uploads/{id}/complete", "/v1/dl/{id}"} {
		if _, ok := paths[path]; !ok {
			t.Fatalf("openapi missing path %s", path)
		}
	}

	docs := ts.request(t, http.MethodGet, "/docs", nil, nil)
	if docs.Code != http.StatusOK || !strings.Contains(docs.Body.String(), "FileHub API") {
		t.Fatalf("docs status=%d body head=%q", docs.Code, docs.Body.String()[:min(80, docs.Body.Len())])
	}

	metrics := ts.request(t, http.MethodGet, "/metrics", nil, nil)
	if metrics.Code != http.StatusOK {
		t.Fatalf("metrics status=%d body=%s", metrics.Code, metrics.Body.String())
	}
	body := metrics.Body.String()
	for _, marker := range []string{
		"filehub_requests_total",
		"filehub_request_duration_seconds_bucket",
		"filehub_upload_bytes_total",
		"filehub_direct_uploads_total",
		"filehub_storage_bytes",
		"filehub_thumbnail_cache_hits_total 1",
		`filehub_assets_total{purpose="media",status="all"} 1`,
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("metrics missing %q in:\n%s", marker, body)
		}
	}
}

func TestStaticAssetRoutesSupportGetAndHead(t *testing.T) {
	ts := newTestServer(t)
	entries, err := web.StaticFS.ReadDir("static/assets")
	if err != nil {
		t.Fatal(err)
	}
	var asset string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || (!strings.HasSuffix(name, ".css") && !strings.HasSuffix(name, ".js")) {
			continue
		}
		asset = name
		break
	}
	if asset == "" {
		t.Skip("no built JS/CSS static asset present")
	}

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			rec := ts.request(t, method, "/assets/"+asset, nil, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s /assets/%s status=%d body=%s", method, asset, rec.Code, rec.Body.String())
			}
			contentType := rec.Header().Get("Content-Type")
			if strings.HasPrefix(contentType, "text/html") {
				t.Fatalf("%s /assets/%s Content-Type=%q, want static asset MIME", method, asset, contentType)
			}
		})
	}
}

func TestStaticIndexUsesBasePathAssetURLs(t *testing.T) {
	ts := newTestServer(t)

	rec := ts.request(t, http.MethodGet, "/", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, marker := range []string{`src="/assets/`, `href="/assets/`, `src="/static/assets/`, `href="/static/assets/`} {
		if strings.Contains(body, marker) {
			t.Fatalf("index.html contains non-relocatable asset URL %q in:\n%s", marker, body)
		}
	}
	if !strings.Contains(body, `/filehub/assets/`) {
		t.Fatalf("index.html does not contain /filehub/ base path asset URLs:\n%s", body)
	}
}

type testServer struct {
	router *gin.Engine
	db     *gormstore.Store
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	cfg := config.Defaults()
	cfg.APIKeyAuthEnabled = true
	cfg.APIKeys = []string{"test-key"}
	return newTestServerWithConfig(t, cfg)
}

func newTestServerWithConfig(t *testing.T, cfg config.Config) *testServer {
	t.Helper()
	ctx := context.Background()
	cfg.DSN = "sqlite://" + filepath.Join(t.TempDir(), "filehub.db")
	def := config.Defaults()
	if cfg.Storage.Backend == "" || (cfg.Storage.Backend == config.BackendOSFS && cfg.Storage.DataDir == def.Storage.DataDir) {
		cfg.Storage.Backend = config.BackendMemFS
	}
	if cfg.APIKeys != nil {
		cfg.APIKeys = []string{"test-key"}
	}
	cfg.PresignSecret = "test-secret"
	cfg.RatePerSec = 10000
	cfg.RateBurst = 10000
	db, err := gormstore.Open(ctx, cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	blobs, err := storage.New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	pipeline := processing.New(4, blobs, db, nil)
	router := NewRouter(RouterDeps{Config: cfg, Assets: db, Uploads: db, AIReviews: db, Reviews: db, Storage: blobs, Pipeline: pipeline})
	return &testServer{router: router, db: db}
}

func (ts *testServer) uploadAsset(t *testing.T, target, filename string, data []byte, fields map[string]string) map[string]any {
	t.Helper()
	rec := ts.uploadAssetStatus(t, target, filename, data, fields)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func (ts *testServer) uploadAssetStatus(t *testing.T, target, filename string, data []byte, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return ts.uploadAssetStatusWithContentType(t, target, filename, "", data, fields)
}

func (ts *testServer) uploadAssetWithContentType(t *testing.T, target, filename, contentType string, data []byte, fields map[string]string) map[string]any {
	t.Helper()
	rec := ts.uploadAssetStatusWithContentType(t, target, filename, contentType, data, fields)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func (ts *testServer) uploadAssetStatusWithContentType(t *testing.T, target, filename, contentType string, data []byte, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	var part io.Writer
	var err error
	if contentType == "" {
		part, err = writer.CreateFormFile("file", filename)
	} else {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
		header.Set("Content-Type", contentType)
		part, err = writer.CreatePart(header)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return ts.request(t, http.MethodPost, target, &body, map[string]string{"Content-Type": writer.FormDataContentType()})
}

func (ts *testServer) getJSON(t *testing.T, target string) map[string]any {
	t.Helper()
	rec := ts.request(t, http.MethodGet, target, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status=%d body=%s", target, rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func (ts *testServer) request(t *testing.T, method, target string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Authorization", "Bearer test-key")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	ts.router.ServeHTTP(rec, req)
	return rec
}

func (ts *testServer) waitReady(t *testing.T, id string) {
	t.Helper()
	ts.waitStatus(t, id, "ready")
}

func (ts *testServer) waitStatus(t *testing.T, id, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec := ts.request(t, http.MethodGet, "/v1/assets/"+id, nil, nil)
		if rec.Code == http.StatusOK {
			var out map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out["status"] == want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("asset %s did not become %s", id, want)
}

func withExternalTestHooks(t *testing.T) {
	t.Helper()
	oldValidator := validateExternalHost
	oldDelays := externalRetryDelays
	validateExternalHost = func(host string) error { return nil }
	externalRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() {
		validateExternalHost = oldValidator
		externalRetryDelays = oldDelays
	})
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 4), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func wavBytes() []byte {
	const (
		sampleRate    = 8000
		channels      = 1
		bitsPerSample = 16
		samples       = 8000
	)
	dataSize := samples * channels * bitsPerSample / 8
	out := make([]byte, 44+dataSize)
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], uint32(36+dataSize))
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	binary.LittleEndian.PutUint32(out[16:20], 16)
	binary.LittleEndian.PutUint16(out[20:22], 1)
	binary.LittleEndian.PutUint16(out[22:24], channels)
	binary.LittleEndian.PutUint32(out[24:28], sampleRate)
	binary.LittleEndian.PutUint32(out[28:32], sampleRate*channels*bitsPerSample/8)
	binary.LittleEndian.PutUint16(out[32:34], channels*bitsPerSample/8)
	binary.LittleEndian.PutUint16(out[34:36], bitsPerSample)
	copy(out[36:40], "data")
	binary.LittleEndian.PutUint32(out[40:44], uint32(dataSize))
	return out
}

type fakeS3Server struct {
	URL             string
	server          *httptest.Server
	mu              sync.Mutex
	objects         map[string][]byte
	contentTypes    map[string]string
	uploads         map[string]map[int][]byte
	createCalls     atomic.Int64
	uploadPartCalls atomic.Int64
	completeCalls   atomic.Int64
	abortCalls      atomic.Int64
	copyCalls       atomic.Int64
}

func newFakeS3Server(t *testing.T) *fakeS3Server {
	t.Helper()
	f := &fakeS3Server{
		objects:      map[string][]byte{},
		contentTypes: map[string]string{},
		uploads:      map[string]map[int][]byte{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	f.URL = f.server.URL
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeS3Server) handle(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/test-bucket/")
	switch {
	case r.Method == http.MethodPost && strings.Contains(r.URL.RawQuery, "uploads"):
		f.createCalls.Add(1)
		uploadID := "upl-" + shortID()
		f.mu.Lock()
		f.uploads[uploadID] = map[int][]byte{}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/xml")
		_ = xml.NewEncoder(w).Encode(struct {
			XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
			Bucket   string   `xml:"Bucket"`
			Key      string   `xml:"Key"`
			UploadID string   `xml:"UploadId"`
		}{Bucket: "test-bucket", Key: key, UploadID: uploadID})
	case r.Method == http.MethodPut && r.URL.Query().Get("uploadId") != "":
		f.uploadPartCalls.Add(1)
		partNum, _ := strconv.Atoi(r.URL.Query().Get("partNumber"))
		data, _ := io.ReadAll(r.Body)
		sum := md5.Sum(data)
		etag := hex.EncodeToString(sum[:])
		f.mu.Lock()
		f.uploads[r.URL.Query().Get("uploadId")][partNum] = data
		f.mu.Unlock()
		w.Header().Set("ETag", `"`+etag+`"`)
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "":
		f.copyCalls.Add(1)
		rawSource, _ := url.PathUnescape(r.Header.Get("X-Amz-Copy-Source"))
		sourceKey := strings.TrimPrefix(strings.TrimPrefix(rawSource, "/"), "test-bucket/")
		f.mu.Lock()
		data, ok := f.objects[sourceKey]
		contentType := f.contentTypes[sourceKey]
		if ok {
			f.objects[key] = append([]byte(nil), data...)
			f.contentTypes[key] = contentType
		}
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<CopyObjectResult><ETag>"fake-etag"</ETag></CopyObjectResult>`))
	case r.Method == http.MethodPut:
		data, _ := io.ReadAll(r.Body)
		sum := md5.Sum(data)
		etag := hex.EncodeToString(sum[:])
		f.mu.Lock()
		f.objects[key] = data
		f.contentTypes[key] = r.Header.Get("Content-Type")
		f.mu.Unlock()
		w.Header().Set("ETag", `"`+etag+`"`)
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPost && r.URL.Query().Get("uploadId") != "":
		f.completeCalls.Add(1)
		uploadID := r.URL.Query().Get("uploadId")
		f.mu.Lock()
		partNums := make([]int, 0, len(f.uploads[uploadID]))
		for partNum := range f.uploads[uploadID] {
			partNums = append(partNums, partNum)
		}
		sort.Ints(partNums)
		var full bytes.Buffer
		for _, partNum := range partNums {
			full.Write(f.uploads[uploadID][partNum])
		}
		f.objects[key] = full.Bytes()
		f.contentTypes[key] = "text/plain"
		delete(f.uploads, uploadID)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/xml")
		_ = xml.NewEncoder(w).Encode(struct {
			XMLName xml.Name `xml:"CompleteMultipartUploadResult"`
			Bucket  string   `xml:"Bucket"`
			Key     string   `xml:"Key"`
			ETag    string   `xml:"ETag"`
		}{Bucket: "test-bucket", Key: key, ETag: `"fake-etag"`})
	case r.Method == http.MethodDelete && r.URL.Query().Get("uploadId") != "":
		f.abortCalls.Add(1)
		f.mu.Lock()
		delete(f.uploads, r.URL.Query().Get("uploadId"))
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodDelete:
		f.mu.Lock()
		delete(f.objects, key)
		delete(f.contentTypes, key)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodHead:
		f.mu.Lock()
		data, ok := f.objects[key]
		contentType := f.contentTypes[key]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodGet:
		f.mu.Lock()
		data, ok := f.objects[key]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	default:
		http.Error(w, "not implemented", http.StatusNotImplemented)
	}
}

func TestRouterCreateReviewInvokesHook(t *testing.T) {
	ts := newTestServer(t)

	first := ts.uploadAsset(t, "/v1/assets?on_duplicate=allow", "hook-review.txt", []byte("payload"), map[string]string{"purpose": "media"})
	firstID := first["id"].(string)

	var gotEvent filehubnotify.ReviewCreatedEvent
	done := make(chan struct{})
	hook := func(event filehubnotify.ReviewCreatedEvent) {
		gotEvent = event
		close(done)
	}

	cfg := config.Defaults()
	cfg.APIKeyAuthEnabled = true
	cfg.APIKeys = []string{"test-key"}
	cfg.DSN = "sqlite://" + filepath.Join(t.TempDir(), "filehub-hook.db")
	cfg.Storage.Backend = config.BackendMemFS
	cfg.PresignSecret = "test-secret"
	cfg.RatePerSec = 10000
	cfg.RateBurst = 10000
	ctx := context.Background()
	db, err := gormstore.Open(ctx, cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	blobs, err := storage.New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	pipeline := processing.New(4, blobs, db, nil)
	router := NewRouter(RouterDeps{Config: cfg, Assets: db, Uploads: db, AIReviews: db, Reviews: db, Storage: blobs, Pipeline: pipeline, ReviewCreatedHook: hook})
	server := &testServer{router: router, db: db}

	upload := server.uploadAsset(t, "/v1/assets?on_duplicate=allow", "hook-asset.txt", []byte("data"), map[string]string{"purpose": "media"})
	uploadID := upload["id"].(string)

	create := server.request(t, http.MethodPost, "/v1/reviews", bytes.NewBufferString(`{
		"title":"Hook review",
		"reviewer":"bob",
		"reference_asset_id":"`+uploadID+`",
		"asset_ids":["`+uploadID+`"],
		"trace_id":"trace-hook"
	}`), map[string]string{"Content-Type": "application/json"})
	if create.Code != http.StatusOK {
		t.Fatalf("create review status=%d body=%s", create.Code, create.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	reviewID := created["id"].(string)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ReviewCreatedHook not called")
	}
	if gotEvent.ReviewID != reviewID {
		t.Fatalf("ReviewID=%q want %q", gotEvent.ReviewID, reviewID)
	}
	if gotEvent.Title != "Hook review" {
		t.Fatalf("Title=%q want Hook review", gotEvent.Title)
	}
	if gotEvent.Reviewer != "bob" {
		t.Fatalf("Reviewer=%q want bob", gotEvent.Reviewer)
	}
	if gotEvent.ReferenceID != uploadID {
		t.Fatalf("ReferenceID=%q want %q", gotEvent.ReferenceID, uploadID)
	}
	if len(gotEvent.AssetIDs) != 1 || gotEvent.AssetIDs[0] != uploadID {
		t.Fatalf("AssetIDs=%v want [%s]", gotEvent.AssetIDs, uploadID)
	}
	_ = firstID
	_ = ts
}
