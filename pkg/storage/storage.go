package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/mojatter/s2"
	_ "github.com/mojatter/s2/fs"
	_ "github.com/mojatter/s2/s3"
	"github.com/saker-ai/filehub/pkg/config"
)

// Store is the facade over blob storage and presigning. Data-plane operations
// delegate to a Backend (s2Backend for osfs/memfs, s3Backend for S3/OSS); the
// native multipart capability is exposed only when the backend implements
// MultipartBackend. Store retains the presign secret and base URL for local
// signed-download URL construction.
type Store struct {
	backend       Backend
	multipart     MultipartBackend // nil when the backend has no native multipart
	presignSecret string
	baseURL       string
}

// New creates a Store backed by the configured storage backend.
func New(ctx context.Context, cfg config.Config) (*Store, error) {
	prefix := strings.Trim(strings.TrimSpace(cfg.Storage.Prefix), "/")
	switch cfg.Storage.Backend {
	case "", config.BackendOSFS, config.BackendMemFS:
		s2Cfg, err := buildS2Config(cfg.Storage)
		if err != nil {
			return nil, err
		}
		raw, err := s2.NewStorage(ctx, s2Cfg)
		if err != nil {
			return nil, fmt.Errorf("create storage: %w", err)
		}
		return &Store{
			backend:       newS2Backend(raw, prefix),
			baseURL:       listenBaseURL(cfg.Addr),
			presignSecret: cfg.PresignSecret,
		}, nil
	case config.BackendS3, config.BackendOSS:
		if cfg.Storage.S3Bucket == "" {
			return nil, fmt.Errorf("storage %s backend requires s3_bucket", cfg.Storage.Backend)
		}
		client, err := newS3Client(ctx, cfg.Storage)
		if err != nil {
			return nil, err
		}
		presignClient := client
		if cfg.Storage.S3PublicEndpoint != "" {
			presignClient, err = newS3ClientWithEndpoint(ctx, cfg.Storage, cfg.Storage.S3PublicEndpoint)
			if err != nil {
				return nil, err
			}
		}
		bk := newS3Backend(client, s3.NewPresignClient(presignClient), cfg.Storage.S3Bucket, prefix)
		return &Store{
			backend:       bk,
			multipart:     bk,
			baseURL:       listenBaseURL(cfg.Addr),
			presignSecret: cfg.PresignSecret,
		}, nil
	default:
		return nil, fmt.Errorf("unknown storage backend %q", cfg.Storage.Backend)
	}
}

// Backend returns the underlying backend (for capability checks / type asserts
// to MultipartBackend).
func (s *Store) Backend() Backend { return s.backend }

func buildS2Config(cfg config.StorageConfig) (s2.Config, error) {
	switch cfg.Backend {
	case "", config.BackendOSFS:
		dataDir := cfg.DataDir
		if dataDir == "" {
			dataDir = ".synapse/stack/filehub-data"
		}
		abs, err := filepath.Abs(dataDir)
		if err != nil {
			return s2.Config{}, fmt.Errorf("storage abs path: %w", err)
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return s2.Config{}, fmt.Errorf("storage create dir: %w", err)
		}
		return s2.Config{Type: s2.TypeOSFS, Root: abs}, nil
	case config.BackendMemFS:
		return s2.Config{Type: s2.TypeMemFS}, nil
	case config.BackendS3, config.BackendOSS:
		if cfg.S3Bucket == "" {
			return s2.Config{}, fmt.Errorf("storage %s backend requires s3_bucket", cfg.Backend)
		}
		return s2.Config{
			Type: s2.TypeS3,
			Root: cfg.S3Bucket,
			S3: &s2.S3Config{
				EndpointURL:     cfg.S3Endpoint,
				Region:          cfg.S3Region,
				AccessKeyID:     cfg.S3AccessKey,
				SecretAccessKey: cfg.S3SecretKey,
			},
		}, nil
	default:
		return s2.Config{}, fmt.Errorf("unknown storage backend %q", cfg.Backend)
	}
}

func newS3Client(ctx context.Context, cfg config.StorageConfig) (*s3.Client, error) {
	return newS3ClientWithEndpoint(ctx, cfg, cfg.S3Endpoint)
}

func newS3ClientWithEndpoint(ctx context.Context, cfg config.StorageConfig, endpoint string) (*s3.Client, error) {
	var opts []func(*awsconfig.LoadOptions) error
	if cfg.S3Region != "" {
		opts = append(opts, awsconfig.WithRegion(cfg.S3Region))
	}
	if cfg.S3AccessKey != "" && cfg.S3SecretKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.S3AccessKey, cfg.S3SecretKey, "")))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load s3 config: %w", err)
	}
	var s3Opts []func(*s3.Options)
	if endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = cfg.Backend != config.BackendOSS
			if cfg.Backend == config.BackendOSS {
				o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
			}
		})
	}
	return s3.NewFromConfig(awsCfg, s3Opts...), nil
}

func listenBaseURL(addr string) string {
	host := addr
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	return "http://" + host
}

// Data-plane delegates.

func (s *Store) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	return s.backend.Put(ctx, key, r)
}

func (s *Store) PutBytes(ctx context.Context, key string, data []byte) error {
	return s.backend.PutBytes(ctx, key, data)
}

func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.backend.Get(ctx, key)
}

func (s *Store) ReadAll(ctx context.Context, key string) ([]byte, error) {
	return s.backend.ReadAll(ctx, key)
}

func (s *Store) Delete(ctx context.Context, key string) error {
	return s.backend.Delete(ctx, key)
}

func (s *Store) Promote(ctx context.Context, sourceKey, targetKey string) error {
	return s.backend.Promote(ctx, sourceKey, targetKey)
}

func (s *Store) DeleteRecursive(ctx context.Context, prefix string) error {
	return s.backend.DeleteRecursive(ctx, prefix)
}

func (s *Store) Exists(ctx context.Context, key string) (bool, error) {
	return s.backend.Exists(ctx, key)
}

func (s *Store) HeadObject(ctx context.Context, key string) (*ObjectInfo, error) {
	return s.backend.HeadObject(ctx, key)
}

func (s *Store) PutThumbnail(ctx context.Context, assetID string, w, h int, format string, data []byte) error {
	return s.backend.PutBytes(ctx, ThumbnailKey(assetID, w, h, format), data)
}

func (s *Store) GetThumbnail(ctx context.Context, assetID string, w, h int, format string) (io.ReadCloser, error) {
	return s.backend.Get(ctx, ThumbnailKey(assetID, w, h, format))
}

// Native multipart delegates (ErrNotSupported when the backend has no native
// multipart capability).

func (s *Store) NativeMultipartSupported() bool { return s.multipart != nil }

func (s *Store) PresignObjectURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if s.multipart == nil {
		return "", fmt.Errorf("%w: native presign unavailable", ErrNotSupported)
	}
	return s.multipart.PresignObjectURL(ctx, key, ttl)
}

func (s *Store) PresignPutObject(ctx context.Context, key, contentType string, ttl time.Duration) (*PresignedRequest, error) {
	if s.multipart == nil {
		return nil, fmt.Errorf("%w: native upload presign unavailable", ErrNotSupported)
	}
	return s.multipart.PresignPutObject(ctx, key, contentType, ttl)
}

func (s *Store) PresignUploadPart(ctx context.Context, key, uploadID string, partNum int, ttl time.Duration) (*PresignedRequest, error) {
	if s.multipart == nil {
		return nil, fmt.Errorf("%w: native upload presign unavailable", ErrNotSupported)
	}
	return s.multipart.PresignUploadPart(ctx, key, uploadID, partNum, ttl)
}

func (s *Store) CreateMultipartUpload(ctx context.Context, key, contentType string) (string, error) {
	if s.multipart == nil {
		return "", fmt.Errorf("%w: native multipart unavailable", ErrNotSupported)
	}
	return s.multipart.CreateMultipartUpload(ctx, key, contentType)
}

func (s *Store) UploadPart(ctx context.Context, key, uploadID string, partNum int, r io.Reader) (string, int64, error) {
	if s.multipart == nil {
		return "", 0, fmt.Errorf("%w: native multipart unavailable", ErrNotSupported)
	}
	return s.multipart.UploadPart(ctx, key, uploadID, partNum, r)
}

func (s *Store) CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []MultipartPart) error {
	if s.multipart == nil {
		return fmt.Errorf("%w: native multipart unavailable", ErrNotSupported)
	}
	return s.multipart.CompleteMultipartUpload(ctx, key, uploadID, parts)
}

func (s *Store) AbortMultipartUpload(ctx context.Context, key, uploadID string) error {
	if s.multipart == nil || uploadID == "" {
		return nil
	}
	return s.multipart.AbortMultipartUpload(ctx, key, uploadID)
}

// Local signed-download URL construction (presign secret + base URL retained
// on Store; backends are not concerned with this).

func (s *Store) LocalPresignURL(tenantID, assetID string, expires time.Time) string {
	expiresUnix := expires.Unix()
	if tenantID == "" {
		tenantID = "default"
	}
	sig := s.SignTenant(tenantID, assetID, expiresUnix)
	u := fmt.Sprintf("%s/v1/dl/%s?tenant_id=%s&expires=%d&sig=%s", strings.TrimRight(s.baseURL, "/"), url.PathEscape(assetID), url.QueryEscape(tenantID), expiresUnix, sig)
	return u
}

func (s *Store) Sign(assetID string, expiresUnix int64) string {
	mac := hmac.New(sha256.New, []byte(s.presignSecret))
	_, _ = fmt.Fprintf(mac, "%s|%d", assetID, expiresUnix)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Store) SignTenant(tenantID, assetID string, expiresUnix int64) string {
	if tenantID == "" {
		tenantID = "default"
	}
	mac := hmac.New(sha256.New, []byte(s.presignSecret))
	_, _ = fmt.Fprintf(mac, "%s|%s|%d", tenantID, assetID, expiresUnix)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Store) Verify(assetID string, expiresUnix int64, sig string) bool {
	want, err := hex.DecodeString(s.Sign(assetID, expiresUnix))
	if err != nil {
		return false
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	return hmac.Equal(got, want)
}

func (s *Store) VerifyTenant(tenantID, assetID string, expiresUnix int64, sig string) bool {
	if tenantID == "" {
		tenantID = "default"
	}
	want, err := hex.DecodeString(s.SignTenant(tenantID, assetID, expiresUnix))
	if err != nil {
		return false
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	return hmac.Equal(got, want)
}

// seekableBody returns a ReadSeeker for r (spooling to a temp file when r is not
// seekable) so the S3 SDK can retry/rewind the body. Used by s3Backend.
func seekableBody(r io.Reader) (io.ReadSeeker, int64, func(), error) {
	if seeker, ok := r.(io.ReadSeeker); ok {
		start, err := seeker.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, 0, func() {}, err
		}
		end, err := seeker.Seek(0, io.SeekEnd)
		if err != nil {
			return nil, 0, func() {}, err
		}
		if _, err := seeker.Seek(start, io.SeekStart); err != nil {
			return nil, 0, func() {}, err
		}
		return seeker, end - start, func() {}, nil
	}
	tmp, err := os.CreateTemp("", "filehub-s3-body-*")
	if err != nil {
		return nil, 0, func() {}, err
	}
	ok := false
	defer func() {
		if !ok {
			name := tmp.Name()
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	n, err := io.Copy(tmp, r)
	if err != nil {
		return nil, 0, func() {}, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, 0, func() {}, err
	}
	ok = true
	return tmp, n, func() {
		name := tmp.Name()
		_ = tmp.Close()
		_ = os.Remove(name)
	}, nil
}

func ThumbnailKey(assetID string, w, h int, format string) string {
	if format == "" {
		format = "jpg"
	}
	return fmt.Sprintf("_thumbs/%s/%dx%d.%s", assetID, w, h, strings.TrimPrefix(format, "."))
}

// PreviewKey identifies a cached rendered preview (currently a PDF converted
// from office documents via LibreOffice).
func PreviewKey(assetID string) string {
	return fmt.Sprintf("_previews/%s/preview.pdf", assetID)
}

func ChunkKey(uploadID string, partNum int) string {
	return fmt.Sprintf("_chunks/%s/part-%d", uploadID, partNum)
}

func ChunkPrefix(uploadID string) string {
	return fmt.Sprintf("_chunks/%s/", uploadID)
}

func ReaderFromBytes(data []byte) io.Reader {
	return bytes.NewReader(data)
}
