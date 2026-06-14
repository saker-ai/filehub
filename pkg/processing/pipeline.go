package processing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/imaging"
	"github.com/rwcarlsen/goexif/exif"
	"github.com/saker-ai/assethub/pkg/storage"
	"github.com/saker-ai/assethub/pkg/store"
)

type Pipeline struct {
	sem     chan struct{}
	wg      sync.WaitGroup
	storage *storage.Store
	repo    store.AssetRepo
	logger  *slog.Logger
	observe func(time.Duration)
}

func New(concurrency int, storage *storage.Store, repo store.AssetRepo, logger *slog.Logger) *Pipeline {
	if concurrency <= 0 {
		concurrency = 8
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Pipeline{
		sem:     make(chan struct{}, concurrency),
		storage: storage,
		repo:    repo,
		logger:  logger,
	}
}

func (p *Pipeline) ObserveProcessing(fn func(time.Duration)) {
	p.observe = fn
}

func (p *Pipeline) Enqueue(ctx context.Context, asset *store.Asset) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		select {
		case p.sem <- struct{}{}:
			defer func() { <-p.sem }()
		case <-ctx.Done():
			return
		}
		if err := p.Process(ctx, asset); err != nil {
			p.logger.Warn("asset processing failed", "asset_id", asset.ID, "error", err)
			_ = p.repo.UpdateStatus(context.Background(), asset.ID, "error")
		}
	}()
}

func (p *Pipeline) Process(ctx context.Context, asset *store.Asset) error {
	start := time.Now()
	defer func() {
		if p.observe != nil {
			p.observe(time.Since(start))
		}
	}()
	if err := p.repo.UpdateStatus(ctx, asset.ID, "processing"); err != nil {
		return err
	}
	data, err := p.storage.ReadAll(ctx, asset.StorageKey)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	asset.Checksum = "sha256:" + hex.EncodeToString(sum[:])
	if asset.Metadata == nil {
		asset.Metadata = store.JSONMap{}
	}
	if strings.HasPrefix(asset.ContentType, "image/") {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err == nil {
			asset.Metadata["exif.width"] = cfg.Width
			asset.Metadata["exif.height"] = cfg.Height
		}
		extractEXIF(asset.Metadata, data)
		_ = p.generateImageThumbnail(ctx, asset.ID, data, 256, 256, "jpg")
	} else {
		enrichMetadata(ctx, asset, data)
	}
	asset.Status = "ready"
	return p.repo.Update(ctx, asset)
}

func extractEXIF(metadata store.JSONMap, data []byte) {
	x, err := exif.Decode(bytes.NewReader(data))
	if err != nil {
		return
	}
	addString := func(key string, field exif.FieldName) {
		tag, err := x.Get(field)
		if err != nil {
			return
		}
		value, err := tag.StringVal()
		if err != nil || value == "" {
			return
		}
		metadata[key] = value
	}
	addString("exif.camera_make", exif.Make)
	addString("exif.camera_model", exif.Model)
	addString("exif.date_time", exif.DateTime)
	addString("exif.date_time_original", exif.DateTimeOriginal)
	if tag, err := x.Get(exif.ColorSpace); err == nil {
		if value, err := tag.Int(0); err == nil {
			metadata["exif.color_space"] = value
		}
	}
	if lat, long, err := x.LatLong(); err == nil {
		metadata["exif.gps_latitude"] = lat
		metadata["exif.gps_longitude"] = long
	}
}

func (p *Pipeline) Wait() {
	p.wg.Wait()
}

func (p *Pipeline) GenerateThumbnail(ctx context.Context, asset *store.Asset, w, h int, format string) (io.ReadCloser, string, error) {
	if format == "" {
		format = "webp"
	}
	if w <= 0 {
		w = 256
	}
	if h <= 0 {
		h = 256
	}
	if rc, err := p.storage.GetThumbnail(ctx, asset.ID, w, h, format); err == nil {
		return rc, "image/" + format, nil
	}
	if !strings.HasPrefix(asset.ContentType, "image/") && !strings.HasPrefix(asset.ContentType, "video/") {
		return nil, "", store.ErrNotFound
	}
	data, err := p.storage.ReadAll(ctx, asset.StorageKey)
	if err != nil {
		return nil, "", err
	}
	if strings.HasPrefix(asset.ContentType, "video/") {
		if err := p.generateVideoThumbnail(ctx, asset.ID, asset.Filename, data, w, h, format); err != nil {
			return nil, "", err
		}
	} else {
		if err := p.generateImageThumbnail(ctx, asset.ID, data, w, h, format); err != nil {
			return nil, "", err
		}
	}
	rc, err := p.storage.GetThumbnail(ctx, asset.ID, w, h, format)
	if err != nil {
		return nil, "", err
	}
	return rc, thumbnailContentType(format), nil
}

func (p *Pipeline) generateImageThumbnail(ctx context.Context, assetID string, data []byte, w, h int, format string) error {
	img, err := imaging.Decode(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("decode image: %w", err)
	}
	thumb := imaging.Fill(img, w, h, imaging.Center, imaging.Lanczos)
	var buf bytes.Buffer
	switch format {
	case "png":
		err = imaging.Encode(&buf, thumb, imaging.PNG)
	case "webp":
		err = encodeWebP(ctx, &buf, thumb)
	default:
		format = "jpg"
		err = imaging.Encode(&buf, thumb, imaging.JPEG)
	}
	if err != nil {
		return fmt.Errorf("encode thumbnail: %w", err)
	}
	return p.storage.PutThumbnail(ctx, assetID, w, h, format, buf.Bytes())
}

func (p *Pipeline) generateVideoThumbnail(ctx context.Context, assetID, filename string, data []byte, w, h int, format string) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return store.ErrNotFound
	}
	if format != "png" && format != "webp" {
		format = "jpg"
	}
	input, cleanupInput, err := writeTempMedia(data, filename)
	if err != nil {
		return err
	}
	defer cleanupInput()
	output, err := os.CreateTemp("", "assethub-frame-*."+format)
	if err != nil {
		return err
	}
	outputName := output.Name()
	_ = output.Close()
	defer func() { _ = os.Remove(outputName) }()
	if err := runFFmpegFrame(ctx, input, outputName, w, h); err != nil {
		return err
	}
	frame, err := os.ReadFile(outputName)
	if err != nil {
		return err
	}
	return p.storage.PutThumbnail(ctx, assetID, w, h, format, frame)
}

func encodeWebP(ctx context.Context, dst *bytes.Buffer, img image.Image) error {
	var png bytes.Buffer
	if err := imaging.Encode(&png, img, imaging.PNG); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "image2pipe", "-vcodec", "png", "-i", "pipe:0", "-vcodec", "libwebp", "-f", "webp", "pipe:1")
	cmd.Stdin = &png
	cmd.Stdout = dst
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg webp encode: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func thumbnailContentType(format string) string {
	switch format {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}
