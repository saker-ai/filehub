package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadYAMLAndEnvOverrides(t *testing.T) {
	t.Setenv("ASSETHUB_MAX_CONCURRENT_UPLOADS", "7")
	t.Setenv("ASSETHUB_MAX_STORAGE_BYTES", "2048")
	t.Setenv("ASSETHUB_PRESIGN_TTL", "30m")

	path := filepath.Join(t.TempDir(), "assethub.yaml")
	if err := os.WriteFile(path, []byte(`
addr: "127.0.0.1:18040"
dsn: "sqlite://custom.db"
presign_ttl: 10m
max_upload_bytes: 1024
max_storage_bytes: 512
max_concurrent_uploads: 3
storage:
  backend: memfs
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:18040" {
		t.Fatalf("addr = %q", cfg.Addr)
	}
	if cfg.Storage.Backend != BackendMemFS {
		t.Fatalf("storage backend = %q", cfg.Storage.Backend)
	}
	if cfg.MaxUploadBytes != 1024 {
		t.Fatalf("max upload bytes = %d", cfg.MaxUploadBytes)
	}
	if cfg.MaxStorageBytes != 2048 {
		t.Fatalf("max storage bytes = %d", cfg.MaxStorageBytes)
	}
	if cfg.MaxConcurrentUploads != 7 {
		t.Fatalf("max concurrent uploads = %d", cfg.MaxConcurrentUploads)
	}
	if cfg.PresignTTL != 30*time.Minute {
		t.Fatalf("presign ttl = %s", cfg.PresignTTL)
	}
}

func TestDefaultsPresignTTL(t *testing.T) {
	cfg := Defaults()
	if cfg.PresignTTL != 7*24*time.Hour {
		t.Fatalf("presign ttl = %s", cfg.PresignTTL)
	}
}

func TestDefaultsDisableAssetHubAPIKeyAuth(t *testing.T) {
	cfg := Defaults()
	if cfg.APIKeyAuthEnabled {
		t.Fatal("API key auth enabled by default")
	}
	if len(cfg.APIKeys) != 0 {
		t.Fatalf("api keys = %v, want empty defaults", cfg.APIKeys)
	}
}

func TestLoadRejectsInternalAuthWithoutMasterSecret(t *testing.T) {
	t.Setenv("ASSETHUB_INTERNAL_AUTH_ENABLED", "true")

	_, err := Load("")
	if err == nil {
		t.Fatal("Load succeeded without internal auth master secret")
	}
	if !strings.Contains(err.Error(), "internal_auth.master_secret") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadRejectsEnabledAPIKeyAuthWithoutKeys(t *testing.T) {
	t.Setenv("ASSETHUB_API_KEY_AUTH_ENABLED", "true")

	_, err := Load("")
	if err == nil {
		t.Fatal("Load succeeded with API key auth enabled and no keys")
	}
	if !strings.Contains(err.Error(), "api_keys") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadOSSEnvAliases(t *testing.T) {
	t.Setenv("ASSETHUB_STORAGE_BACKEND", BackendOSS)
	t.Setenv("ASSETHUB_OSS_ENDPOINT", "https://oss.example.com")
	t.Setenv("ASSETHUB_OSS_REGION", "cn-hangzhou")
	t.Setenv("ASSETHUB_OSS_BUCKET", "assets")
	t.Setenv("ASSETHUB_OSS_ACCESS_KEY", "ak")
	t.Setenv("ASSETHUB_OSS_SECRET_KEY", "sk")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.Backend != BackendOSS {
		t.Fatalf("backend = %q", cfg.Storage.Backend)
	}
	if cfg.Storage.S3Endpoint != "https://oss.example.com" ||
		cfg.Storage.S3Region != "cn-hangzhou" ||
		cfg.Storage.S3Bucket != "assets" ||
		cfg.Storage.S3AccessKey != "ak" ||
		cfg.Storage.S3SecretKey != "sk" {
		t.Fatalf("oss aliases not mapped: %#v", cfg.Storage)
	}
}

func TestLoadS3EnvTakesPrecedenceOverOSSAliases(t *testing.T) {
	t.Setenv("ASSETHUB_S3_ENDPOINT", "https://s3.example.com")
	t.Setenv("ASSETHUB_OSS_ENDPOINT", "https://oss.example.com")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.S3Endpoint != "https://s3.example.com" {
		t.Fatalf("s3 endpoint = %q", cfg.Storage.S3Endpoint)
	}
}
