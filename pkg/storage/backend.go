package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// Backend abstracts blob object storage operations across local (osfs/memfs)
// and native object stores (S3/OSS). Implementations handle key prefixing
// internally so callers pass logical keys.
type Backend interface {
	Put(ctx context.Context, key string, r io.Reader) (int64, error)
	PutBytes(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	ReadAll(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
	Promote(ctx context.Context, sourceKey, targetKey string) error
	DeleteRecursive(ctx context.Context, prefix string) error
	Exists(ctx context.Context, key string) (bool, error)
	HeadObject(ctx context.Context, key string) (*ObjectInfo, error)
	NativeMultipartSupported() bool
}

// MultipartBackend extends Backend with native multipart upload and presign
// capabilities (S3/OSS). Backends without native multipart (osfs/memfs) do
// not implement this interface; Store multipart methods return ErrNotSupported
// for them.
type MultipartBackend interface {
	Backend
	PresignObjectURL(ctx context.Context, key string, ttl time.Duration) (string, error)
	PresignPutObject(ctx context.Context, key, contentType string, ttl time.Duration) (*PresignedRequest, error)
	PresignUploadPart(ctx context.Context, key, uploadID string, partNum int, ttl time.Duration) (*PresignedRequest, error)
	CreateMultipartUpload(ctx context.Context, key, contentType string) (string, error)
	UploadPart(ctx context.Context, key, uploadID string, partNum int, r io.Reader) (string, int64, error)
	CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []MultipartPart) error
	AbortMultipartUpload(ctx context.Context, key, uploadID string) error
}

// ErrNotSupported is returned when a backend does not support a native
// operation (e.g. multipart/presign on osfs/memfs).
var ErrNotSupported = errors.New("operation not supported by storage backend")

// MultipartPart is one part of a completed multipart upload.
type MultipartPart struct {
	PartNum int
	ETag    string
}

// PresignedRequest is a short-lived direct URL to an object.
type PresignedRequest struct {
	URL     string
	Header  map[string]string
	Expires time.Time
}

// ObjectInfo is metadata returned by HeadObject.
type ObjectInfo struct {
	Bytes       int64
	ContentType string
	ETag        string
}
