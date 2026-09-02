package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// s3Backend implements Backend + MultipartBackend over the native AWS S3 SDK
// (S3/OSS). It unifies S3 and OSS (the only difference is path-style, decided
// at client construction). All data-plane CRUD goes directly through the S3
// client — no s2 indirection.
type s3Backend struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
	prefix  string
}

func newS3Backend(client *s3.Client, presign *s3.PresignClient, bucket, prefix string) *s3Backend {
	return &s3Backend{client: client, presign: presign, bucket: bucket, prefix: prefix}
}

func (b *s3Backend) objectKey(key string) string {
	key = strings.TrimLeft(path.Clean("/"+key), "/")
	if b.prefix == "" {
		return key
	}
	return path.Join(b.prefix, key)
}

func (b *s3Backend) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	body, bytesWritten, cleanup, err := seekableBody(r)
	if err != nil {
		return 0, fmt.Errorf("prepare object body: %w", err)
	}
	defer cleanup()
	if _, err := b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(b.objectKey(key)),
		Body:   body,
	}); err != nil {
		return 0, fmt.Errorf("put object: %w", err)
	}
	return bytesWritten, nil
}

func (b *s3Backend) PutBytes(ctx context.Context, key string, data []byte) error {
	if _, err := b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(b.objectKey(key)),
		Body:   bytes.NewReader(data),
	}); err != nil {
		return fmt.Errorf("put object: %w", err)
	}
	return nil
}

func (b *s3Backend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(b.objectKey(key)),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil, fmt.Errorf("get object: %w", ErrObjectNotFound)
		}
		return nil, fmt.Errorf("get object: %w", err)
	}
	return out.Body, nil
}

func (b *s3Backend) ReadAll(ctx context.Context, key string) ([]byte, error) {
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

func (b *s3Backend) Delete(ctx context.Context, key string) error {
	if _, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(b.objectKey(key)),
	}); err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

func (b *s3Backend) DeleteRecursive(ctx context.Context, prefix string) error {
	keyPrefix := b.objectKey(prefix)
	paginator := s3.NewListObjectsV2Paginator(b.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(b.bucket),
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
		if _, err := b.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(b.bucket),
			Delete: &s3types.Delete{Objects: objects},
		}); err != nil {
			return fmt.Errorf("delete prefix: %w", err)
		}
	}
	return nil
}

func (b *s3Backend) Exists(ctx context.Context, key string) (bool, error) {
	_, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(b.objectKey(key)),
	})
	if err != nil {
		if isS3NotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("exists object: %w", err)
	}
	return true, nil
}

func (b *s3Backend) HeadObject(ctx context.Context, key string) (*ObjectInfo, error) {
	out, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(b.objectKey(key)),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil, fmt.Errorf("head object: %w", ErrObjectNotFound)
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

// Promote copies a staged object to its final key via CopyObject and removes
// the staged object. Idempotent when the source is gone and the target exists.
func (b *s3Backend) Promote(ctx context.Context, sourceKey, targetKey string) error {
	if sourceKey == "" || targetKey == "" {
		return fmt.Errorf("promote object: source and target keys are required")
	}
	if sourceKey == targetKey {
		return nil
	}
	sourceExists, err := b.Exists(ctx, sourceKey)
	if err != nil {
		return fmt.Errorf("promote object source: %w", err)
	}
	if !sourceExists {
		targetExists, targetErr := b.Exists(ctx, targetKey)
		if targetErr != nil {
			return fmt.Errorf("promote object target: %w", targetErr)
		}
		if targetExists {
			return nil
		}
		return fmt.Errorf("promote object source: %w", ErrObjectNotFound)
	}
	copySource := url.PathEscape(path.Join(b.bucket, b.objectKey(sourceKey)))
	if _, err := b.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(b.bucket),
		CopySource: aws.String(copySource),
		Key:        aws.String(b.objectKey(targetKey)),
	}); err != nil {
		return fmt.Errorf("promote object copy: %w", err)
	}
	if err := b.Delete(ctx, sourceKey); err != nil {
		return fmt.Errorf("promote object cleanup: %w", err)
	}
	return nil
}

func (b *s3Backend) NativeMultipartSupported() bool { return true }

func (b *s3Backend) PresignObjectURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	out, err := b.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(b.objectKey(key)),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign object: %w", err)
	}
	return out.URL, nil
}

func (b *s3Backend) PresignPutObject(ctx context.Context, key, contentType string, ttl time.Duration) (*PresignedRequest, error) {
	input := &s3.PutObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(b.objectKey(key)),
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	out, err := b.presign.PresignPutObject(ctx, input, s3.WithPresignExpires(ttl))
	if err != nil {
		return nil, fmt.Errorf("presign put object: %w", err)
	}
	return presignedRequest(out.URL, out.SignedHeader, ttl), nil
}

func (b *s3Backend) PresignUploadPart(ctx context.Context, key, uploadID string, partNum int, ttl time.Duration) (*PresignedRequest, error) {
	out, err := b.presign.PresignUploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(b.bucket),
		Key:        aws.String(b.objectKey(key)),
		UploadId:   aws.String(uploadID),
		PartNumber: aws.Int32(int32(partNum)),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return nil, fmt.Errorf("presign upload part: %w", err)
	}
	return presignedRequest(out.URL, out.SignedHeader, ttl), nil
}

func (b *s3Backend) CreateMultipartUpload(ctx context.Context, key, contentType string) (string, error) {
	input := &s3.CreateMultipartUploadInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(b.objectKey(key)),
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	out, err := b.client.CreateMultipartUpload(ctx, input)
	if err != nil {
		return "", fmt.Errorf("create multipart upload: %w", err)
	}
	if out.UploadId == nil || *out.UploadId == "" {
		return "", fmt.Errorf("create multipart upload: empty upload id")
	}
	return *out.UploadId, nil
}

func (b *s3Backend) UploadPart(ctx context.Context, key, uploadID string, partNum int, r io.Reader) (string, int64, error) {
	body, bytesWritten, cleanup, err := seekableBody(r)
	if err != nil {
		return "", 0, fmt.Errorf("prepare multipart part: %w", err)
	}
	defer cleanup()
	out, err := b.client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(b.bucket),
		Key:        aws.String(b.objectKey(key)),
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

func (b *s3Backend) CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []MultipartPart) error {
	completed := make([]s3types.CompletedPart, 0, len(parts))
	for _, part := range parts {
		completed = append(completed, s3types.CompletedPart{
			ETag:       aws.String(part.ETag),
			PartNumber: aws.Int32(int32(part.PartNum)),
		})
	}
	if _, err := b.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(b.bucket),
		Key:      aws.String(b.objectKey(key)),
		UploadId: aws.String(uploadID),
		MultipartUpload: &s3types.CompletedMultipartUpload{
			Parts: completed,
		},
	}); err != nil {
		return fmt.Errorf("complete multipart upload: %w", err)
	}
	return nil
}

func (b *s3Backend) AbortMultipartUpload(ctx context.Context, key, uploadID string) error {
	if uploadID == "" {
		return nil
	}
	if _, err := b.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(b.bucket),
		Key:      aws.String(b.objectKey(key)),
		UploadId: aws.String(uploadID),
	}); err != nil && !isS3NotFound(err) {
		return fmt.Errorf("abort multipart upload: %w", err)
	}
	return nil
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

func presignedRequest(rawURL string, signedHeader http.Header, ttl time.Duration) *PresignedRequest {
	header := map[string]string{}
	for key, values := range signedHeader {
		if len(values) > 0 {
			header[key] = values[0]
		}
	}
	return &PresignedRequest{URL: rawURL, Header: header, Expires: time.Now().Add(ttl)}
}
