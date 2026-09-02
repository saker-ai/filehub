package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/mojatter/s2"
)

// s2Backend implements Backend over the s2.Storage abstraction (osfs/memfs).
// It does not support native multipart upload; Promote uses a Get+Put+Delete
// fallback.
type s2Backend struct {
	store  s2.Storage
	prefix string
}

func newS2Backend(store s2.Storage, prefix string) *s2Backend {
	return &s2Backend{store: store, prefix: prefix}
}

func (b *s2Backend) objectKey(key string) string {
	key = strings.TrimLeft(path.Clean("/"+key), "/")
	if b.prefix == "" {
		return key
	}
	return path.Join(b.prefix, key)
}

func (b *s2Backend) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, fmt.Errorf("read object: %w", err)
	}
	if err := b.store.Put(ctx, s2.NewObjectBytes(b.objectKey(key), data)); err != nil {
		return 0, fmt.Errorf("put object: %w", err)
	}
	return int64(len(data)), nil
}

func (b *s2Backend) PutBytes(ctx context.Context, key string, data []byte) error {
	if err := b.store.Put(ctx, s2.NewObjectBytes(b.objectKey(key), data)); err != nil {
		return fmt.Errorf("put object: %w", err)
	}
	return nil
}

func (b *s2Backend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := b.store.Get(ctx, b.objectKey(key))
	if err != nil {
		if errors.Is(err, s2.ErrNotExist) {
			return nil, fmt.Errorf("get object: %w", ErrObjectNotFound)
		}
		return nil, fmt.Errorf("get object: %w", err)
	}
	rc, err := obj.Open()
	if err != nil {
		return nil, fmt.Errorf("open object: %w", err)
	}
	return rc, nil
}

func (b *s2Backend) ReadAll(ctx context.Context, key string) ([]byte, error) {
	rc, err := b.Get(ctx, key)
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

func (b *s2Backend) Delete(ctx context.Context, key string) error {
	if err := b.store.Delete(ctx, b.objectKey(key)); err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

func (b *s2Backend) DeleteRecursive(ctx context.Context, prefix string) error {
	if err := b.store.DeleteRecursive(ctx, b.objectKey(prefix)); err != nil {
		return fmt.Errorf("delete prefix: %w", err)
	}
	return nil
}

func (b *s2Backend) Exists(ctx context.Context, key string) (bool, error) {
	ok, err := b.store.Exists(ctx, b.objectKey(key))
	if err != nil {
		return false, fmt.Errorf("exists object: %w", err)
	}
	return ok, nil
}

func (b *s2Backend) HeadObject(ctx context.Context, key string) (*ObjectInfo, error) {
	data, err := b.ReadAll(ctx, key)
	if err != nil {
		return nil, err
	}
	return &ObjectInfo{Bytes: int64(len(data))}, nil
}

// Promote copies a staged object to its final key and removes the staged
// object. Idempotent when the source is gone and the target exists, so upload
// completion can recover after a process crash.
func (b *s2Backend) Promote(ctx context.Context, sourceKey, targetKey string) error {
	if sourceKey == "" || targetKey == "" {
		return fmt.Errorf("promote object: source and target keys are required")
	}
	if sourceKey == targetKey {
		return nil
	}
	rc, err := b.Get(ctx, sourceKey)
	if err != nil {
		targetExists, targetErr := b.Exists(ctx, targetKey)
		if targetErr == nil && targetExists {
			return nil
		}
		return fmt.Errorf("promote object source: %w", errors.Join(err, targetErr))
	}
	_, putErr := b.Put(ctx, targetKey, rc)
	closeErr := rc.Close()
	if putErr != nil || closeErr != nil {
		return fmt.Errorf("promote object copy: %w", errors.Join(putErr, closeErr))
	}
	if err := b.Delete(ctx, sourceKey); err != nil {
		return fmt.Errorf("promote object cleanup: %w", err)
	}
	return nil
}

func (b *s2Backend) NativeMultipartSupported() bool { return false }
