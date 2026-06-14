package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/saker-ai/internaljwt"
	"gopkg.in/yaml.v3"
)

const (
	BackendOSFS  = "osfs"
	BackendS3    = "s3"
	BackendOSS   = "oss"
	BackendMemFS = "memfs"
)

type Config struct {
	Addr                  string             `json:"addr" yaml:"addr"`
	LogLevel              string             `json:"log_level" yaml:"log_level"`
	DSN                   string             `json:"dsn" yaml:"dsn"`
	Storage               StorageConfig      `json:"storage" yaml:"storage"`
	APIKeyAuthEnabled     bool               `json:"api_key_auth_enabled" yaml:"api_key_auth_enabled"`
	APIKeys               []string           `json:"api_keys" yaml:"api_keys"`
	InternalAuth          InternalAuthConfig `json:"internal_auth" yaml:"internal_auth"`
	PresignSecret         string             `json:"presign_secret" yaml:"presign_secret"`
	PresignTTL            time.Duration      `json:"presign_ttl" yaml:"presign_ttl"`
	MaxUploadBytes        int64              `json:"max_upload_bytes" yaml:"max_upload_bytes"`
	MaxStorageBytes       int64              `json:"max_storage_bytes" yaml:"max_storage_bytes"`
	MaxConcurrentUploads  int                `json:"max_concurrent_uploads" yaml:"max_concurrent_uploads"`
	ChunkUploadMaxAge     time.Duration      `json:"chunk_upload_max_age" yaml:"chunk_upload_max_age"`
	ExternalFetchTimeout  time.Duration      `json:"external_fetch_timeout" yaml:"external_fetch_timeout"`
	ExternalFetchMaxSize  int64              `json:"external_fetch_max_size" yaml:"external_fetch_max_size"`
	ProcessingConcurrency int                `json:"processing_concurrency" yaml:"processing_concurrency"`
	GCInterval            time.Duration      `json:"gc_interval" yaml:"gc_interval"`
	CORSOrigins           []string           `json:"cors_origins" yaml:"cors_origins"`
	RatePerSec            int                `json:"rate_per_sec" yaml:"rate_per_sec"`
	RateBurst             int                `json:"rate_burst" yaml:"rate_burst"`
	MetricsEnabled        bool               `json:"metrics_enabled" yaml:"metrics_enabled"`
	MetricsPath           string             `json:"metrics_path" yaml:"metrics_path"`
	WebEnabled            bool               `json:"web_enabled" yaml:"web_enabled"`
}

type InternalAuthConfig struct {
	Enabled                    bool          `json:"enabled" yaml:"enabled"`
	Issuer                     string        `json:"issuer" yaml:"issuer"`
	Audience                   string        `json:"audience" yaml:"audience"`
	MasterSecret               string        `json:"master_secret" yaml:"master_secret"`
	TTL                        time.Duration `json:"ttl" yaml:"ttl"`
	ClockSkew                  time.Duration `json:"clock_skew" yaml:"clock_skew"`
	AllowAuthorizationFallback bool          `json:"allow_authorization_fallback" yaml:"allow_authorization_fallback"`
}

type StorageConfig struct {
	Backend     string `json:"backend" yaml:"backend"`
	DataDir     string `json:"data_dir" yaml:"data_dir"`
	Prefix      string `json:"prefix" yaml:"prefix"`
	S3Endpoint  string `json:"s3_endpoint" yaml:"s3_endpoint"`
	S3Region    string `json:"s3_region" yaml:"s3_region"`
	S3Bucket    string `json:"s3_bucket" yaml:"s3_bucket"`
	S3AccessKey string `json:"s3_access_key" yaml:"s3_access_key"`
	S3SecretKey string `json:"s3_secret_key" yaml:"s3_secret_key"`
}

func Load(path string) (Config, error) {
	cfg := Defaults()
	if strings.TrimSpace(path) != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		if strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml") {
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				return Config{}, fmt.Errorf("parse config yaml: %w", err)
			}
		} else if err := json.Unmarshal(data, &cfg); err != nil {
			if yamlErr := yaml.Unmarshal(data, &cfg); yamlErr != nil {
				return Config{}, fmt.Errorf("parse config json: %w", err)
			}
		}
	}
	applyEnv(&cfg)
	cfg.withDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Defaults() Config {
	return Config{
		Addr:                  ":17040",
		LogLevel:              "info",
		DSN:                   "sqlite://.synapse/stack/assethub.db",
		Storage:               StorageConfig{Backend: BackendOSFS, DataDir: ".synapse/stack/assethub-data"},
		APIKeys:               nil,
		InternalAuth:          InternalAuthConfig{Issuer: "synapse", Audience: "assethub", TTL: 5 * time.Minute},
		PresignSecret:         "assethub-presign-secret",
		PresignTTL:            7 * 24 * time.Hour,
		MaxUploadBytes:        512 * 1024 * 1024,
		MaxConcurrentUploads:  10,
		ChunkUploadMaxAge:     24 * time.Hour,
		ExternalFetchTimeout:  5 * time.Minute,
		ExternalFetchMaxSize:  1024 * 1024 * 1024,
		ProcessingConcurrency: 8,
		GCInterval:            time.Hour,
		CORSOrigins:           []string{"*"},
		RatePerSec:            100,
		RateBurst:             200,
		MetricsEnabled:        true,
		MetricsPath:           "/metrics",
		WebEnabled:            true,
	}
}

func (c *Config) withDefaults() {
	def := Defaults()
	if c.Addr == "" {
		c.Addr = def.Addr
	}
	if c.LogLevel == "" {
		c.LogLevel = def.LogLevel
	}
	if c.DSN == "" {
		c.DSN = def.DSN
	}
	if c.Storage.Backend == "" {
		c.Storage.Backend = def.Storage.Backend
	}
	if c.Storage.DataDir == "" {
		c.Storage.DataDir = def.Storage.DataDir
	}
	if c.InternalAuth.Issuer == "" {
		c.InternalAuth.Issuer = def.InternalAuth.Issuer
	}
	if c.InternalAuth.Audience == "" {
		c.InternalAuth.Audience = def.InternalAuth.Audience
	}
	if c.InternalAuth.TTL <= 0 {
		c.InternalAuth.TTL = def.InternalAuth.TTL
	}
	if c.PresignSecret == "" {
		c.PresignSecret = def.PresignSecret
	}
	if c.PresignTTL <= 0 {
		c.PresignTTL = def.PresignTTL
	}
	if c.MaxUploadBytes <= 0 {
		c.MaxUploadBytes = def.MaxUploadBytes
	}
	if c.MaxConcurrentUploads <= 0 {
		c.MaxConcurrentUploads = def.MaxConcurrentUploads
	}
	if c.ChunkUploadMaxAge <= 0 {
		c.ChunkUploadMaxAge = def.ChunkUploadMaxAge
	}
	if c.ExternalFetchTimeout <= 0 {
		c.ExternalFetchTimeout = def.ExternalFetchTimeout
	}
	if c.ExternalFetchMaxSize <= 0 {
		c.ExternalFetchMaxSize = def.ExternalFetchMaxSize
	}
	if c.ProcessingConcurrency <= 0 {
		c.ProcessingConcurrency = def.ProcessingConcurrency
	}
	if c.GCInterval <= 0 {
		c.GCInterval = def.GCInterval
	}
	if len(c.CORSOrigins) == 0 {
		c.CORSOrigins = def.CORSOrigins
	}
	if c.RatePerSec <= 0 {
		c.RatePerSec = def.RatePerSec
	}
	if c.RateBurst <= 0 {
		c.RateBurst = def.RateBurst
	}
	if c.MetricsPath == "" {
		c.MetricsPath = def.MetricsPath
	}
}

func (c Config) Validate() error {
	if c.InternalAuth.Enabled {
		if strings.TrimSpace(c.InternalAuth.Issuer) == "" {
			return fmt.Errorf("internal_auth.issuer is required when internal_auth.enabled=true")
		}
		if strings.TrimSpace(c.InternalAuth.Audience) == "" {
			return fmt.Errorf("internal_auth.audience is required when internal_auth.enabled=true")
		}
		if len(internaljwt.NormalizeMasterSecret(c.InternalAuth.MasterSecret)) < 32 {
			return fmt.Errorf("internal_auth.master_secret must be at least 32 bytes when internal_auth.enabled=true")
		}
	}
	if c.APIKeyAuthEnabled && len(c.APIKeys) == 0 {
		return fmt.Errorf("api_keys is required when api_key_auth_enabled=true")
	}
	return nil
}

func applyEnv(c *Config) {
	setString := func(key string, dst *string) {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			*dst = v
		}
	}
	setStringIfEmpty := func(key string, dst *string) {
		if strings.TrimSpace(*dst) == "" {
			setString(key, dst)
		}
	}
	setString("ASSETHUB_ADDR", &c.Addr)
	setString("ASSETHUB_DSN", &c.DSN)
	setString("ASSETHUB_LOG_LEVEL", &c.LogLevel)
	setString("ASSETHUB_STORAGE_BACKEND", &c.Storage.Backend)
	setString("ASSETHUB_STORAGE_DIR", &c.Storage.DataDir)
	setString("ASSETHUB_STORAGE_PREFIX", &c.Storage.Prefix)
	setString("ASSETHUB_S3_ENDPOINT", &c.Storage.S3Endpoint)
	setString("ASSETHUB_S3_REGION", &c.Storage.S3Region)
	setString("ASSETHUB_S3_BUCKET", &c.Storage.S3Bucket)
	setString("ASSETHUB_S3_ACCESS_KEY", &c.Storage.S3AccessKey)
	setString("ASSETHUB_S3_SECRET_KEY", &c.Storage.S3SecretKey)
	setStringIfEmpty("ASSETHUB_OSS_ENDPOINT", &c.Storage.S3Endpoint)
	setStringIfEmpty("ASSETHUB_OSS_REGION", &c.Storage.S3Region)
	setStringIfEmpty("ASSETHUB_OSS_BUCKET", &c.Storage.S3Bucket)
	setStringIfEmpty("ASSETHUB_OSS_ACCESS_KEY", &c.Storage.S3AccessKey)
	setStringIfEmpty("ASSETHUB_OSS_SECRET_KEY", &c.Storage.S3SecretKey)
	setString("ASSETHUB_PRESIGN_SECRET", &c.PresignSecret)
	setBool("ASSETHUB_API_KEY_AUTH_ENABLED", &c.APIKeyAuthEnabled)
	if v := csvEnv("ASSETHUB_API_KEYS"); len(v) > 0 {
		c.APIKeys = v
	}
	setBool("ASSETHUB_INTERNAL_AUTH_ENABLED", &c.InternalAuth.Enabled)
	setString("ASSETHUB_INTERNAL_AUTH_ISSUER", &c.InternalAuth.Issuer)
	setString("ASSETHUB_INTERNAL_AUTH_AUDIENCE", &c.InternalAuth.Audience)
	setString("ASSETHUB_INTERNAL_AUTH_MASTER_SECRET", &c.InternalAuth.MasterSecret)
	setBool("ASSETHUB_INTERNAL_AUTH_ALLOW_AUTHORIZATION_FALLBACK", &c.InternalAuth.AllowAuthorizationFallback)
	setDuration("ASSETHUB_INTERNAL_AUTH_TTL", &c.InternalAuth.TTL)
	setDuration("ASSETHUB_INTERNAL_AUTH_CLOCK_SKEW", &c.InternalAuth.ClockSkew)
	if v := csvEnv("ASSETHUB_CORS_ORIGINS"); len(v) > 0 {
		c.CORSOrigins = v
	}
	setDuration("ASSETHUB_PRESIGN_TTL", &c.PresignTTL)
	setDuration("ASSETHUB_CHUNK_UPLOAD_MAX_AGE", &c.ChunkUploadMaxAge)
	setDuration("ASSETHUB_EXTERNAL_FETCH_TIMEOUT", &c.ExternalFetchTimeout)
	setDuration("ASSETHUB_GC_INTERVAL", &c.GCInterval)
	setInt64("ASSETHUB_MAX_UPLOAD_BYTES", &c.MaxUploadBytes)
	setInt64("ASSETHUB_MAX_STORAGE_BYTES", &c.MaxStorageBytes)
	setInt("ASSETHUB_MAX_CONCURRENT_UPLOADS", &c.MaxConcurrentUploads)
	setInt64("ASSETHUB_EXTERNAL_FETCH_MAX_SIZE", &c.ExternalFetchMaxSize)
	setInt("ASSETHUB_PROCESSING_CONCURRENCY", &c.ProcessingConcurrency)
	setInt("ASSETHUB_RATE_PER_SEC", &c.RatePerSec)
	setInt("ASSETHUB_RATE_BURST", &c.RateBurst)
	setBool("ASSETHUB_METRICS_ENABLED", &c.MetricsEnabled)
	setString("ASSETHUB_METRICS_PATH", &c.MetricsPath)
	setBool("ASSETHUB_WEB_ENABLED", &c.WebEnabled)
}

func csvEnv(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func setDuration(key string, dst *time.Duration) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return
	}
	v, err := time.ParseDuration(raw)
	if err == nil {
		*dst = v
	}
}

func setInt(key string, dst *int) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return
	}
	v, err := strconv.Atoi(raw)
	if err == nil {
		*dst = v
	}
}

func setInt64(key string, dst *int64) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err == nil {
		*dst = v
	}
}

func setBool(key string, dst *bool) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return
	}
	v, err := strconv.ParseBool(raw)
	if err == nil {
		*dst = v
	}
}
