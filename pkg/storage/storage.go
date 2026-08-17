package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/mojatter/s2"
	_ "github.com/mojatter/s2/fs"
	_ "github.com/mojatter/s2/s3"
	"github.com/saker-ai/filehub/pkg/config"
)

type Store struct {
	s2            s2.Storage
	backend       string
	prefix        string
	presignSecret string
	baseURL       string
	s3Bucket      string
	s3Client      *s3.Client
	s3Presign     *s3.PresignClient
}

type MultipartPart struct {
	PartNum int
	ETag    string
}

type PresignedRequest struct {
	URL     string
	Header  map[string]string
	Expires time.Time
}

type ObjectInfo struct {
	Bytes       int64
	ContentType string
	ETag        string
}

func New(ctx context.Context, cfg config.Config) (*Store, error) {
	s2Cfg, err := buildS2Config(cfg.Storage)
	if err != nil {
		return nil, err
	}
	raw, err := s2.NewStorage(ctx, s2Cfg)
	if err != nil {
		return nil, fmt.Errorf("create storage: %w", err)
	}
	out := &Store{
		s2:            raw,
		backend:       cfg.Storage.Backend,
		prefix:        strings.Trim(strings.TrimSpace(cfg.Storage.Prefix), "/"),
		presignSecret: cfg.PresignSecret,
		baseURL:       listenBaseURL(cfg.Addr),
		s3Bucket:      cfg.Storage.S3Bucket,
	}
	if cfg.Storage.Backend == config.BackendS3 || cfg.Storage.Backend == config.BackendOSS {
		client, err := newS3Client(ctx, cfg.Storage)
		if err != nil {
			return nil, err
		}
		out.s3Client = client
		presignClient := client
		if cfg.Storage.S3PublicEndpoint != "" {
			presignClient, err = newS3ClientWithEndpoint(ctx, cfg.Storage, cfg.Storage.S3PublicEndpoint)
			if err != nil {
				return nil, err
			}
		}
		out.s3Presign = s3.NewPresignClient(presignClient)
	}
	return out, nil
}

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

func (s *Store) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	if s.nativeObjectStore() {
		body, bytesWritten, cleanup, err := seekableBody(r)
		if err != nil {
			return 0, fmt.Errorf("prepare object body: %w", err)
		}
		defer cleanup()
		if _, err := s.s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(s.s3Bucket),
			Key:    aws.String(s.objectKey(key)),
			Body:   body,
		}); err != nil {
			return 0, fmt.Errorf("put object: %w", err)
		}
		return bytesWritten, nil
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, fmt.Errorf("read object: %w", err)
	}
	if err := s.s2.Put(ctx, s2.NewObjectBytes(s.objectKey(key), data)); err != nil {
		return 0, fmt.Errorf("put object: %w", err)
	}
	return int64(len(data)), nil
}

func (s *Store) PutBytes(ctx context.Context, key string, data []byte) error {
	if s.nativeObjectStore() {
		if _, err := s.s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(s.s3Bucket),
			Key:    aws.String(s.objectKey(key)),
			Body:   bytes.NewReader(data),
		}); err != nil {
			return fmt.Errorf("put object: %w", err)
		}
		return nil
	}
	return s.s2.Put(ctx, s2.NewObjectBytes(s.objectKey(key), data))
}

func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if s.nativeObjectStore() {
		out, err := s.s3Client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(s.s3Bucket),
			Key:    aws.String(s.objectKey(key)),
		})
		if err != nil {
			return nil, fmt.Errorf("get object: %w", err)
		}
		return out.Body, nil
	}
	obj, err := s.s2.Get(ctx, s.objectKey(key))
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}
	rc, err := obj.Open()
	if err != nil {
		return nil, fmt.Errorf("open object: %w", err)
	}
	return rc, nil
}

func (s *Store) ReadAll(ctx context.Context, key string) ([]byte, error) {
	rc, err := s.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("read object: %w", err)
	}
	return data, nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	if s.nativeObjectStore() {
		if _, err := s.s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(s.s3Bucket),
			Key:    aws.String(s.objectKey(key)),
		}); err != nil {
			return fmt.Errorf("delete object: %w", err)
		}
		return nil
	}
	if err := s.s2.Delete(ctx, s.objectKey(key)); err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

// Promote copies a staged object to its final key and removes the staged
// object. It is idempotent when the source is gone and the target exists, so
// upload completion can recover after a process crash.
func (s *Store) Promote(ctx context.Context, sourceKey, targetKey string) error {
	if sourceKey == "" || targetKey == "" {
		return fmt.Errorf("promote object: source and target keys are required")
	}
	if sourceKey == targetKey {
		return nil
	}
	if s.NativeMultipartSupported() {
		sourceExists, err := s.Exists(ctx, sourceKey)
		if err != nil {
			return fmt.Errorf("promote object source: %w", err)
		}
		if !sourceExists {
			targetExists, targetErr := s.Exists(ctx, targetKey)
			if targetErr != nil {
				return fmt.Errorf("promote object target: %w", targetErr)
			}
			if targetExists {
				return nil
			}
			return fmt.Errorf("promote object source: %w", os.ErrNotExist)
		}
		copySource := url.PathEscape(path.Join(s.s3Bucket, s.objectKey(sourceKey)))
		if _, err := s.s3Client.CopyObject(ctx, &s3.CopyObjectInput{
			Bucket:     aws.String(s.s3Bucket),
			CopySource: aws.String(copySource),
			Key:        aws.String(s.objectKey(targetKey)),
		}); err != nil {
			return fmt.Errorf("promote object copy: %w", err)
		}
		if err := s.Delete(ctx, sourceKey); err != nil {
			return fmt.Errorf("promote object cleanup: %w", err)
		}
		return nil
	}

	rc, err := s.Get(ctx, sourceKey)
	if err != nil {
		targetExists, targetErr := s.Exists(ctx, targetKey)
		if targetErr == nil && targetExists {
			return nil
		}
		return fmt.Errorf("promote object source: %w", errors.Join(err, targetErr))
	}
	_, putErr := s.Put(ctx, targetKey, rc)
	closeErr := rc.Close()
	if putErr != nil || closeErr != nil {
		return fmt.Errorf("promote object copy: %w", errors.Join(putErr, closeErr))
	}
	if err := s.Delete(ctx, sourceKey); err != nil {
		return fmt.Errorf("promote object cleanup: %w", err)
	}
	return nil
}

func (s *Store) DeleteRecursive(ctx context.Context, prefix string) error {
	if s.nativeObjectStore() {
		keyPrefix := s.objectKey(prefix)
		paginator := s3.NewListObjectsV2Paginator(s.s3Client, &s3.ListObjectsV2Input{
			Bucket: aws.String(s.s3Bucket),
			Prefix: aws.String(keyPrefix),
		})
		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				return fmt.Errorf("list prefix: %w", err)
			}
			if len(page.Contents) == 0 {
				continue
			}
			objects := make([]s3types.ObjectIdentifier, 0, len(page.Contents))
			for _, object := range page.Contents {
				if object.Key == nil {
					continue
				}
				objects = append(objects, s3types.ObjectIdentifier{Key: object.Key})
			}
			if len(objects) == 0 {
				continue
			}
			if _, err := s.s3Client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: aws.String(s.s3Bucket),
				Delete: &s3types.Delete{Objects: objects},
			}); err != nil {
				return fmt.Errorf("delete prefix: %w", err)
			}
		}
		return nil
	}
	if err := s.s2.DeleteRecursive(ctx, s.objectKey(prefix)); err != nil {
		return fmt.Errorf("delete prefix: %w", err)
	}
	return nil
}

func (s *Store) Exists(ctx context.Context, key string) (bool, error) {
	if s.nativeObjectStore() {
		_, err := s.s3Client.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(s.s3Bucket),
			Key:    aws.String(s.objectKey(key)),
		})
		if err != nil {
			if isS3NotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("exists object: %w", err)
		}
		return true, nil
	}
	ok, err := s.s2.Exists(ctx, s.objectKey(key))
	if err != nil {
		return false, fmt.Errorf("exists object: %w", err)
	}
	return ok, nil
}

func (s *Store) PutThumbnail(ctx context.Context, assetID string, w, h int, format string, data []byte) error {
	return s.PutBytes(ctx, ThumbnailKey(assetID, w, h, format), data)
}

func (s *Store) GetThumbnail(ctx context.Context, assetID string, w, h int, format string) (io.ReadCloser, error) {
	return s.Get(ctx, ThumbnailKey(assetID, w, h, format))
}

func (s *Store) PresignObjectURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if !s.NativeMultipartSupported() {
		return "", fmt.Errorf("native presign unavailable for backend %s", s.backend)
	}
	out, err := s.s3Presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.s3Bucket),
		Key:    aws.String(s.objectKey(key)),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign object: %w", err)
	}
	return out.URL, nil
}

func (s *Store) PresignPutObject(ctx context.Context, key, contentType string, ttl time.Duration) (*PresignedRequest, error) {
	if !s.NativeMultipartSupported() {
		return nil, fmt.Errorf("native upload presign unavailable for backend %s", s.backend)
	}
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.s3Bucket),
		Key:    aws.String(s.objectKey(key)),
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	out, err := s.s3Presign.PresignPutObject(ctx, input, s3.WithPresignExpires(ttl))
	if err != nil {
		return nil, fmt.Errorf("presign put object: %w", err)
	}
	return presignedRequest(out.URL, out.SignedHeader, ttl), nil
}

func (s *Store) PresignUploadPart(ctx context.Context, key, uploadID string, partNum int, ttl time.Duration) (*PresignedRequest, error) {
	if !s.NativeMultipartSupported() {
		return nil, fmt.Errorf("native upload presign unavailable for backend %s", s.backend)
	}
	out, err := s.s3Presign.PresignUploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(s.s3Bucket),
		Key:        aws.String(s.objectKey(key)),
		UploadId:   aws.String(uploadID),
		PartNumber: aws.Int32(int32(partNum)),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return nil, fmt.Errorf("presign upload part: %w", err)
	}
	return presignedRequest(out.URL, out.SignedHeader, ttl), nil
}

func (s *Store) HeadObject(ctx context.Context, key string) (*ObjectInfo, error) {
	if s.nativeObjectStore() || s.NativeMultipartSupported() {
		out, err := s.s3Client.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(s.s3Bucket),
			Key:    aws.String(s.objectKey(key)),
		})
		if err != nil {
			if isS3NotFound(err) {
				return nil, fmt.Errorf("head object: %w", os.ErrNotExist)
			}
			return nil, fmt.Errorf("head object: %w", err)
		}
		info := &ObjectInfo{
			ContentType: aws.ToString(out.ContentType),
			ETag:        strings.Trim(aws.ToString(out.ETag), `"`),
		}
		if out.ContentLength != nil {
			info.Bytes = *out.ContentLength
		}
		return info, nil
	}
	data, err := s.ReadAll(ctx, key)
	if err != nil {
		return nil, err
	}
	return &ObjectInfo{Bytes: int64(len(data))}, nil
}

func presignedRequest(rawURL string, signedHeader http.Header, ttl time.Duration) *PresignedRequest {
	header := map[string]string{}
	for key, values := range signedHeader {
		if len(values) > 0 {
			header[key] = values[0]
		}
	}
	return &PresignedRequest{URL: rawURL, Header: header, Expires: time.Now().Add(ttl)}
}

func (s *Store) NativeMultipartSupported() bool {
	return s.s3Client != nil && (s.backend == config.BackendS3 || s.backend == config.BackendOSS)
}

func (s *Store) nativeObjectStore() bool {
	return s.s3Client != nil && s.backend == config.BackendOSS
}

func isS3NotFound(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorCode() {
	case "NoSuchKey", "NoSuchUpload", "NotFound", "NoSuchBucket", "404":
		return true
	default:
		return false
	}
}

func (s *Store) CreateMultipartUpload(ctx context.Context, key, contentType string) (string, error) {
	if !s.NativeMultipartSupported() {
		return "", fmt.Errorf("native multipart unavailable for backend %s", s.backend)
	}
	input := &s3.CreateMultipartUploadInput{
		Bucket: aws.String(s.s3Bucket),
		Key:    aws.String(s.objectKey(key)),
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	out, err := s.s3Client.CreateMultipartUpload(ctx, input)
	if err != nil {
		return "", fmt.Errorf("create multipart upload: %w", err)
	}
	if out.UploadId == nil || *out.UploadId == "" {
		return "", fmt.Errorf("create multipart upload: empty upload id")
	}
	return *out.UploadId, nil
}

func (s *Store) UploadPart(ctx context.Context, key, uploadID string, partNum int, r io.Reader) (string, int64, error) {
	if !s.NativeMultipartSupported() {
		return "", 0, fmt.Errorf("native multipart unavailable for backend %s", s.backend)
	}
	body, bytesWritten, cleanup, err := seekableBody(r)
	if err != nil {
		return "", 0, fmt.Errorf("prepare multipart part: %w", err)
	}
	defer cleanup()
	out, err := s.s3Client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(s.s3Bucket),
		Key:        aws.String(s.objectKey(key)),
		UploadId:   aws.String(uploadID),
		PartNumber: aws.Int32(int32(partNum)),
		Body:       body,
	})
	if err != nil {
		return "", 0, fmt.Errorf("upload part: %w", err)
	}
	etag := ""
	if out.ETag != nil {
		etag = strings.Trim(*out.ETag, `"`)
	}
	return etag, bytesWritten, nil
}

func (s *Store) CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []MultipartPart) error {
	if !s.NativeMultipartSupported() {
		return fmt.Errorf("native multipart unavailable for backend %s", s.backend)
	}
	completed := make([]s3types.CompletedPart, 0, len(parts))
	for _, part := range parts {
		etag := part.ETag
		completed = append(completed, s3types.CompletedPart{
			ETag:       aws.String(etag),
			PartNumber: aws.Int32(int32(part.PartNum)),
		})
	}
	if _, err := s.s3Client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(s.s3Bucket),
		Key:      aws.String(s.objectKey(key)),
		UploadId: aws.String(uploadID),
		MultipartUpload: &s3types.CompletedMultipartUpload{
			Parts: completed,
		},
	}); err != nil {
		return fmt.Errorf("complete multipart upload: %w", err)
	}
	return nil
}

func (s *Store) AbortMultipartUpload(ctx context.Context, key, uploadID string) error {
	if !s.NativeMultipartSupported() || uploadID == "" {
		return nil
	}
	if _, err := s.s3Client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(s.s3Bucket),
		Key:      aws.String(s.objectKey(key)),
		UploadId: aws.String(uploadID),
	}); err != nil && !isS3NotFound(err) {
		return fmt.Errorf("abort multipart upload: %w", err)
	}
	return nil
}

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

func (s *Store) objectKey(key string) string {
	key = strings.TrimLeft(path.Clean("/"+key), "/")
	if s.prefix == "" {
		return key
	}
	return path.Join(s.prefix, key)
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
