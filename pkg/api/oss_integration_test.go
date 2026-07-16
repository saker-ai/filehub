//go:build integration

package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/saker-ai/filehub/pkg/config"
)

func TestRealOSSExternalDirectUpload(t *testing.T) {
	required := []string{"FILEHUB_OSS_ENDPOINT", "FILEHUB_OSS_PUBLIC_ENDPOINT", "FILEHUB_OSS_BUCKET", "FILEHUB_OSS_ACCESS_KEY", "FILEHUB_OSS_SECRET_KEY"}
	for _, name := range required {
		if os.Getenv(name) == "" {
			t.Skip("real OSS integration credentials are not configured")
		}
	}
	cfg := config.Defaults()
	cfg.Storage.Backend = config.BackendOSS
	cfg.Storage.S3Endpoint = os.Getenv("FILEHUB_OSS_ENDPOINT")
	cfg.Storage.S3PublicEndpoint = os.Getenv("FILEHUB_OSS_PUBLIC_ENDPOINT")
	cfg.Storage.S3Bucket = os.Getenv("FILEHUB_OSS_BUCKET")
	cfg.Storage.S3Region = os.Getenv("FILEHUB_OSS_REGION")
	cfg.Storage.S3AccessKey = os.Getenv("FILEHUB_OSS_ACCESS_KEY")
	cfg.Storage.S3SecretKey = os.Getenv("FILEHUB_OSS_SECRET_KEY")
	cfg.APIKeyAuthEnabled = true
	cfg.APIKeys = []string{"test-key"}
	ts := newTestServerWithConfig(t, cfg)

	content := []byte("filehub-real-oss-" + time.Now().UTC().Format(time.RFC3339Nano))
	createBody, _ := json.Marshal(map[string]any{
		"mode": "direct", "filename": "oss-integration.txt", "purpose": "general",
		"contentType": "text/plain", "totalBytes": len(content),
	})
	created := ts.request(t, http.MethodPost, "/v1/external/uploads", bytes.NewReader(createBody), map[string]string{"Content-Type": "application/json"})
	if created.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var session struct {
		UploadID string            `json:"uploadId"`
		AssetID  string            `json:"assetId"`
		URL      string            `json:"url"`
		Headers  map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	put, err := http.NewRequest(http.MethodPut, session.URL, bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range session.Headers {
		put.Header.Set(name, value)
	}
	resp, err := http.DefaultClient.Do(put)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("provider PUT status=%d", resp.StatusCode)
	}
	digest := sha256.Sum256(content)
	completeBody, _ := json.Marshal(map[string]any{"checksum": "sha256:" + hex.EncodeToString(digest[:])})
	completed := ts.request(t, http.MethodPost, "/v1/external/uploads/"+session.UploadID+"/complete", bytes.NewReader(completeBody), map[string]string{"Content-Type": "application/json"})
	if completed.Code != http.StatusOK {
		t.Fatalf("complete status=%d body=%s", completed.Code, completed.Body.String())
	}
	t.Cleanup(func() { _ = ts.request(t, http.MethodDelete, "/v1/assets/"+session.AssetID, nil, nil) })
	contentResp := ts.request(t, http.MethodGet, "/v1/external/assets/"+session.AssetID, nil, nil)
	if contentResp.Code != http.StatusOK || !bytes.Equal(contentResp.Body.Bytes(), content) {
		t.Fatalf("content status=%d body=%q", contentResp.Code, contentResp.Body.String())
	}
}
