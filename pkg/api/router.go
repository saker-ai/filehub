package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/saker-ai/assethub/pkg/config"
	"github.com/saker-ai/assethub/pkg/processing"
	blob "github.com/saker-ai/assethub/pkg/storage"
	"github.com/saker-ai/assethub/pkg/store"
	"github.com/saker-ai/assethub/web"
)

type RouterDeps struct {
	Config   config.Config
	Assets   store.AssetRepo
	Uploads  store.UploadRepo
	Storage  *blob.Store
	Pipeline *processing.Pipeline
	Metrics  *Metrics
}

func NewRouter(deps RouterDeps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	if deps.Metrics == nil {
		deps.Metrics = NewMetrics()
	}
	h := newHandler(deps)
	r := gin.New()
	r.Use(gin.Recovery(), CORSMiddleware(deps.Config.CORSOrigins), RequestID(), RateLimit(deps.Config.RatePerSec, deps.Config.RateBurst), MetricsMiddleware(deps.Metrics), RequestLogger(nil), Auth(deps.Config))
	RegisterHumaDocs(r)
	r.GET("/healthz", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	if deps.Config.MetricsEnabled {
		path := deps.Config.MetricsPath
		if path == "" {
			path = "/metrics"
		}
		r.GET(path, func(c *gin.Context) {
			stats, err := h.assetStats(c.Request.Context(), Tenant(c))
			if err != nil {
				writeErr(c, err)
				return
			}
			c.Data(http.StatusOK, "text/plain; version=0.0.4; charset=utf-8", []byte(deps.Metrics.Render(stats)))
		})
	}
	if deps.Config.WebEnabled {
		staticSub, err := fs.Sub(web.StaticFS, "static")
		if err != nil {
			panic(err)
		}
		staticFS := http.FS(staticSub)
		r.StaticFS("/static", staticFS)
		r.GET("/assets/:file", func(c *gin.Context) {
			c.FileFromFS("assets/"+c.Param("file"), staticFS)
		})
		serveIndex := func(c *gin.Context) {
			data, err := fs.ReadFile(staticSub, "index.html")
			if err != nil {
				writeError(c, http.StatusInternalServerError, "internal_error", "web index not found")
				return
			}
			c.Data(http.StatusOK, "text/html; charset=utf-8", data)
		}
		r.GET("/", func(c *gin.Context) {
			serveIndex(c)
		})
		r.NoRoute(func(c *gin.Context) {
			if strings.HasPrefix(c.Request.URL.Path, "/v1/") {
				writeError(c, http.StatusNotFound, "not_found", "resource not found")
				return
			}
			serveIndex(c)
		})
	}
	v1 := r.Group("/v1")
	v1.POST("/files", h.createFile)
	v1.GET("/files", h.listFiles)
	v1.GET("/files/:id", h.getFile)
	v1.DELETE("/files/:id", h.deleteFile)
	v1.GET("/files/:id/content", h.getContent)
	v1.POST("/assets", h.createAsset)
	v1.GET("/assets", h.listAssets)
	v1.GET("/assets/stats", h.stats)
	v1.POST("/assets/bulk-delete", h.bulkDelete)
	v1.GET("/assets/:id", h.getAsset)
	v1.PATCH("/assets/:id", h.patchAsset)
	v1.DELETE("/assets/:id", h.deleteAsset)
	v1.POST("/assets/:id/presign", h.presign)
	v1.GET("/assets/:id/content", h.getContent)
	v1.GET("/assets/:id/thumbnail", h.thumbnail)
	v1.GET("/dl/:id", h.signedDownload)
	v1.POST("/uploads", h.createUpload)
	v1.PUT("/uploads/:id/parts/:part", h.putPart)
	v1.POST("/uploads/:id/complete", h.completeUpload)
	v1.DELETE("/uploads/:id", h.cancelUpload)
	return r
}

type handler struct {
	deps        RouterDeps
	uploadSlots chan struct{}
	quota       *quotaTracker
	statsCache  *statsCache
}

type quotaTracker struct {
	mu       sync.Mutex
	reserved map[string]int64
}

type statsCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	expires map[string]time.Time
	values  map[string]*store.AssetStats
}

var (
	validateExternalHost = validatePublicHost
	externalRetryDelays  = []time.Duration{5 * time.Second, 15 * time.Second}
	errPayloadTooLarge   = errors.New("payload too large")
)

func newHandler(deps RouterDeps) handler {
	limit := deps.Config.MaxConcurrentUploads
	if limit <= 0 {
		limit = 1
	}
	return handler{
		deps:        deps,
		uploadSlots: make(chan struct{}, limit),
		quota:       &quotaTracker{reserved: map[string]int64{}},
		statsCache:  &statsCache{ttl: 30 * time.Second, expires: map[string]time.Time{}, values: map[string]*store.AssetStats{}},
	}
}

var validPurposes = map[string]bool{
	"assistants": true, "batch": true, "fine-tune": true, "media": true, "vector-store": true, "general": true,
}

func (h handler) acquireUpload(c *gin.Context) bool {
	select {
	case h.uploadSlots <- struct{}{}:
		return true
	default:
		writeError(c, http.StatusTooManyRequests, "concurrent_uploads_exceeded", "too many concurrent uploads")
		return false
	}
}

func (h handler) releaseUpload() {
	select {
	case <-h.uploadSlots:
	default:
	}
}

func (h handler) reserveQuota(c *gin.Context, bytes int64) (func(), bool) {
	release, err := h.reserveTenantQuota(c.Request.Context(), Tenant(c), bytes)
	if err != nil {
		writeErr(c, err)
		return nil, false
	}
	return release, true
}

func (h handler) reserveTenantQuota(ctx context.Context, tenantID string, bytes int64) (func(), error) {
	if bytes <= 0 || h.deps.Config.MaxStorageBytes <= 0 {
		return func() {}, nil
	}
	stats, err := h.deps.Assets.Stats(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	h.quota.mu.Lock()
	defer h.quota.mu.Unlock()
	if stats.TotalBytes+h.quota.reserved[tenantID]+bytes > h.deps.Config.MaxStorageBytes {
		return nil, store.ErrQuotaExceeded
	}
	h.quota.reserved[tenantID] += bytes
	released := false
	return func() {
		h.quota.mu.Lock()
		defer h.quota.mu.Unlock()
		if released {
			return
		}
		h.quota.reserved[tenantID] -= bytes
		if h.quota.reserved[tenantID] <= 0 {
			delete(h.quota.reserved, tenantID)
		}
		released = true
	}, nil
}

func (h handler) assetStats(ctx context.Context, tenantID string) (*store.AssetStats, error) {
	now := time.Now()
	h.statsCache.mu.Lock()
	if cached := h.statsCache.values[tenantID]; cached != nil && now.Before(h.statsCache.expires[tenantID]) {
		out := cloneStats(cached)
		h.statsCache.mu.Unlock()
		return out, nil
	}
	h.statsCache.mu.Unlock()

	stats, err := h.deps.Assets.Stats(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	h.statsCache.mu.Lock()
	h.statsCache.values[tenantID] = cloneStats(stats)
	h.statsCache.expires[tenantID] = now.Add(h.statsCache.ttl)
	h.statsCache.mu.Unlock()
	return stats, nil
}

func (h handler) invalidateStats(tenantID string) {
	h.statsCache.mu.Lock()
	delete(h.statsCache.values, tenantID)
	delete(h.statsCache.expires, tenantID)
	h.statsCache.mu.Unlock()
}

func cloneStats(stats *store.AssetStats) *store.AssetStats {
	if stats == nil {
		return nil
	}
	return &store.AssetStats{
		Total:         stats.Total,
		TotalBytes:    stats.TotalBytes,
		ByPurpose:     cloneCounts(stats.ByPurpose),
		ByContentType: cloneCounts(stats.ByContentType),
		BySource:      cloneCounts(stats.BySource),
		ByStatus:      cloneCounts(stats.ByStatus),
	}
}

func cloneCounts(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (h handler) createFile(c *gin.Context) {
	if !h.acquireUpload(c) {
		return
	}
	defer h.releaseUpload()
	h.createMultipart(c, "file-", true)
}

func (h handler) createAsset(c *gin.Context) {
	if !h.acquireUpload(c) {
		return
	}
	defer h.releaseUpload()
	if strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
		h.createExternal(c)
		return
	}
	h.createMultipart(c, "asset-", false)
}

func (h handler) createMultipart(c *gin.Context, idPrefix string, openAIFile bool) {
	if h.tooLarge(c) {
		return
	}
	purpose := c.PostForm("purpose")
	if !validPurposes[purpose] {
		writeError(c, http.StatusBadRequest, "invalid_purpose", "invalid purpose")
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "file is required")
		return
	}
	defer func() { _ = file.Close() }()
	tmp, size, checksum, sniff, err := spoolToTemp(file, h.deps.Config.MaxUploadBytes)
	if err != nil {
		if errors.Is(err, errPayloadTooLarge) {
			writeError(c, http.StatusRequestEntityTooLarge, "payload_too_large", "file too large")
			return
		}
		writeErr(c, err)
		return
	}
	defer func() {
		name := tmp.Name()
		_ = tmp.Close()
		_ = os.Remove(name)
	}()
	releaseQuota, ok := h.reserveQuota(c, size)
	if !ok {
		return
	}
	quotaCommitted := false
	defer func() {
		if !quotaCommitted {
			releaseQuota()
		}
	}()
	onDuplicate := c.DefaultQuery("on_duplicate", "reject")
	if openAIFile {
		onDuplicate = "allow"
	}
	if onDuplicate != "allow" {
		if existing, err := h.deps.Assets.FindByChecksum(c.Request.Context(), Tenant(c), checksum); err == nil {
			if onDuplicate == "reuse" {
				c.Set(assetIDKey, existing.ID)
				c.JSON(http.StatusOK, assetResponse(existing, openAIFile))
				return
			}
			writeDuplicateError(c, existing.ID)
			return
		} else if !errors.Is(err, store.ErrNotFound) {
			writeErr(c, err)
			return
		}
	}
	id := idPrefix + shortID()
	contentType := header.Header.Get("Content-Type")
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = http.DetectContentType(sniff)
	}
	meta := parseJSONMap(c.PostForm("metadata"))
	source := c.DefaultPostForm("source", "upload")
	expiresAt := parseExpiresIn(c.PostForm("expires_in"))
	tags := parseTags(c.PostForm("tags"))
	storageKey := storageKey(Tenant(c), purpose, id, header.Filename)
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		writeErr(c, err)
		return
	}
	if _, err := h.deps.Storage.Put(c.Request.Context(), storageKey, tmp); err != nil {
		writeErr(c, err)
		return
	}
	h.deps.Metrics.AddUploadBytes(size)
	asset := &store.Asset{
		ID: id, TenantID: Tenant(c), Purpose: purpose, Filename: header.Filename, ContentType: contentType,
		Bytes: size, StorageKey: storageKey, Checksum: checksum, Status: "uploaded", Source: source,
		Metadata: meta, Tags: tagModels(tags), ExpiresAt: expiresAt,
	}
	if onDuplicate == "reject" {
		asset.DedupeKey = &checksum
	}
	if openAIFile {
		asset.Status = "uploaded"
	}
	if err := h.deps.Assets.Create(c.Request.Context(), asset); err != nil {
		if errors.Is(err, store.ErrConflict) && onDuplicate == "reject" {
			_ = h.deps.Storage.Delete(c.Request.Context(), storageKey)
			if existing, findErr := h.deps.Assets.FindByChecksum(c.Request.Context(), Tenant(c), checksum); findErr == nil {
				writeDuplicateError(c, existing.ID)
				return
			}
		}
		writeErr(c, err)
		return
	}
	c.Set(assetIDKey, asset.ID)
	h.invalidateStats(Tenant(c))
	quotaCommitted = true
	releaseQuota()
	if !openAIFile {
		h.deps.Pipeline.Enqueue(context.Background(), asset)
	}
	status := http.StatusOK
	c.JSON(status, assetResponse(asset, openAIFile))
}

func (h handler) createExternal(c *gin.Context) {
	var req struct {
		URL         string         `json:"url"`
		Purpose     string         `json:"purpose"`
		Filename    string         `json:"filename"`
		Tags        []string       `json:"tags"`
		Source      string         `json:"source"`
		Metadata    map[string]any `json:"metadata"`
		ContentType string         `json:"content_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "invalid json body")
		return
	}
	if !validPurposes[req.Purpose] {
		writeError(c, http.StatusBadRequest, "invalid_purpose", "invalid purpose")
		return
	}
	if err := h.validateExternalURL(req.URL); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if req.Filename == "" {
		if u, err := url.Parse(req.URL); err == nil {
			req.Filename = path.Base(u.Path)
		}
	}
	if req.Filename == "" || req.Filename == "." || req.Filename == "/" {
		req.Filename = "external"
	}
	id := "asset-" + shortID()
	key := storageKey(Tenant(c), req.Purpose, id, req.Filename)
	source := req.Source
	if source == "" {
		source = "external-url"
	}
	meta := store.JSONMap(req.Metadata)
	if meta == nil {
		meta = store.JSONMap{}
	}
	meta["external_url"] = req.URL
	asset := &store.Asset{
		ID: id, TenantID: Tenant(c), Purpose: req.Purpose, Filename: req.Filename, ContentType: req.ContentType,
		Bytes: 0, StorageKey: key, Status: "processing", Source: source,
		Metadata: meta, Tags: tagModels(req.Tags),
	}
	if err := h.deps.Assets.Create(c.Request.Context(), asset); err != nil {
		writeErr(c, err)
		return
	}
	c.Set(assetIDKey, asset.ID)
	h.invalidateStats(Tenant(c))
	h.enqueueExternalFetch(context.WithoutCancel(c.Request.Context()), asset, req.URL, req.ContentType)
	c.JSON(http.StatusOK, assetResponse(asset, false))
}

func (h handler) enqueueExternalFetch(ctx context.Context, asset *store.Asset, rawURL, requestedContentType string) {
	go func() {
		attempts := len(externalRetryDelays) + 1
		var lastErr error
		for attempt := 0; attempt < attempts; attempt++ {
			if attempt > 0 {
				delay := externalRetryDelays[attempt-1]
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
			if err := h.completeExternalFetch(ctx, asset, rawURL, requestedContentType); err != nil {
				lastErr = err
				continue
			}
			return
		}
		failed := *asset
		failed.Status = "error"
		if failed.Metadata == nil {
			failed.Metadata = store.JSONMap{}
		}
		if lastErr != nil {
			failed.Metadata["error"] = lastErr.Error()
		}
		if err := h.deps.Assets.Update(context.Background(), &failed); err == nil {
			h.invalidateStats(failed.TenantID)
		}
	}()
}

func (h handler) completeExternalFetch(ctx context.Context, asset *store.Asset, rawURL, requestedContentType string) error {
	data, contentType, err := h.fetchExternal(ctx, rawURL)
	if err != nil {
		return err
	}
	if requestedContentType != "" {
		contentType = requestedContentType
	}
	if int64(len(data)) > h.deps.Config.MaxUploadBytes {
		return fmt.Errorf("file too large")
	}
	releaseQuota, err := h.reserveTenantQuota(ctx, asset.TenantID, int64(len(data)))
	if err != nil {
		return err
	}
	quotaCommitted := false
	defer func() {
		if !quotaCommitted {
			releaseQuota()
		}
	}()
	if _, err := h.deps.Storage.Put(ctx, asset.StorageKey, bytes.NewReader(data)); err != nil {
		return err
	}
	h.deps.Metrics.AddUploadBytes(int64(len(data)))
	updated := *asset
	updated.Bytes = int64(len(data))
	updated.ContentType = contentType
	updated.Checksum = sha256Hex(data)
	updated.Status = "uploaded"
	if err := h.deps.Assets.Update(ctx, &updated); err != nil {
		return err
	}
	quotaCommitted = true
	releaseQuota()
	h.invalidateStats(asset.TenantID)
	h.deps.Pipeline.Enqueue(context.Background(), &updated)
	return nil
}

func (h handler) tooLarge(c *gin.Context) bool {
	if c.Request.ContentLength > h.deps.Config.MaxUploadBytes && h.deps.Config.MaxUploadBytes > 0 {
		writeError(c, http.StatusRequestEntityTooLarge, "payload_too_large", "file too large")
		return true
	}
	return false
}

func (h handler) listFiles(c *gin.Context) {
	h.list(c, true)
}

func (h handler) listAssets(c *gin.Context) {
	h.list(c, false)
}

func (h handler) list(c *gin.Context, files bool) {
	filter := parseFilter(c)
	if files {
		filter.IDPrefix = "file-"
	}
	items, hasMore, err := h.deps.Assets.List(c.Request.Context(), Tenant(c), filter)
	if err != nil {
		writeErr(c, err)
		return
	}
	data := make([]gin.H, 0, len(items))
	for _, a := range items {
		data = append(data, assetResponse(a, files))
	}
	nextCursor := ""
	if hasMore && len(items) > 0 {
		nextCursor = store.CursorFromAsset(items[len(items)-1])
	}
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": data, "has_more": hasMore, "next_cursor": nextCursor})
}

func (h handler) getFile(c *gin.Context)  { h.get(c, true) }
func (h handler) getAsset(c *gin.Context) { h.get(c, false) }

func (h handler) get(c *gin.Context, file bool) {
	a, err := h.deps.Assets.Get(c.Request.Context(), Tenant(c), c.Param("id"))
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, assetResponse(a, file))
}

func (h handler) deleteFile(c *gin.Context)  { h.delete(c, true) }
func (h handler) deleteAsset(c *gin.Context) { h.delete(c, false) }

func (h handler) delete(c *gin.Context, file bool) {
	a, err := h.deps.Assets.Get(c.Request.Context(), Tenant(c), c.Param("id"))
	if err != nil {
		writeErr(c, err)
		return
	}
	_ = h.deps.Storage.Delete(c.Request.Context(), a.StorageKey)
	_ = h.deps.Storage.DeleteRecursive(c.Request.Context(), "_thumbs/"+a.ID+"/")
	if err := h.deps.Assets.Delete(c.Request.Context(), Tenant(c), a.ID); err != nil {
		writeErr(c, err)
		return
	}
	h.invalidateStats(Tenant(c))
	if file {
		c.JSON(http.StatusOK, gin.H{"id": a.ID, "object": "file", "deleted": true})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": a.ID, "object": "asset", "deleted": true})
}

func (h handler) getContent(c *gin.Context) {
	a, err := h.deps.Assets.Get(c.Request.Context(), Tenant(c), c.Param("id"))
	if err != nil {
		writeErr(c, err)
		return
	}
	h.streamAsset(c, a)
}

func (h handler) streamAsset(c *gin.Context, a *store.Asset) {
	if a.Checksum != "" {
		etag := `"` + a.Checksum + `"`
		c.Header("ETag", etag)
		if c.GetHeader("If-None-Match") == etag {
			c.Status(http.StatusNotModified)
			return
		}
	}
	rc, err := h.deps.Storage.Get(c.Request.Context(), a.StorageKey)
	if err != nil {
		writeErr(c, err)
		return
	}
	defer func() { _ = rc.Close() }()
	disposition := "inline"
	if c.Query("download") == "true" {
		disposition = "attachment"
	}
	c.Header("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": a.Filename}))
	c.Header("Cache-Control", "private, max-age=3600")
	c.DataFromReader(http.StatusOK, a.Bytes, a.ContentType, rc, nil)
}

func (h handler) patchAsset(c *gin.Context) {
	a, err := h.deps.Assets.Get(c.Request.Context(), Tenant(c), c.Param("id"))
	if err != nil {
		writeErr(c, err)
		return
	}
	var req struct {
		Tags     []string       `json:"tags"`
		Metadata map[string]any `json:"metadata"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "invalid json body")
		return
	}
	if req.Metadata != nil {
		a.Metadata = store.JSONMap(req.Metadata)
		if err := h.deps.Assets.Update(c.Request.Context(), a); err != nil {
			writeErr(c, err)
			return
		}
	}
	if req.Tags != nil {
		if err := h.deps.Assets.SetTags(c.Request.Context(), a.ID, req.Tags); err != nil {
			writeErr(c, err)
			return
		}
		a.Tags = tagModels(req.Tags)
	}
	c.JSON(http.StatusOK, assetResponse(a, false))
}

func (h handler) presign(c *gin.Context) {
	a, err := h.deps.Assets.Get(c.Request.Context(), Tenant(c), c.Param("id"))
	if err != nil {
		writeErr(c, err)
		return
	}
	var req struct {
		ExpiresIn string `json:"expires_in"`
	}
	_ = c.ShouldBindJSON(&req)
	ttl := h.deps.Config.PresignTTL
	if req.ExpiresIn != "" {
		if parsed, err := time.ParseDuration(req.ExpiresIn); err == nil {
			ttl = parsed
		}
	}
	expires := time.Now().Add(ttl)
	url, err := h.deps.Storage.PresignObjectURL(c.Request.Context(), a.StorageKey, ttl)
	if err != nil {
		url = h.deps.Storage.LocalPresignURL(a.ID, expires)
	}
	c.JSON(http.StatusOK, gin.H{"url": url, "expires_at": expires.Unix()})
}

func (h handler) signedDownload(c *gin.Context) {
	expires, err := strconv.ParseInt(c.Query("expires"), 10, 64)
	if err != nil || time.Now().Unix() > expires {
		writeError(c, http.StatusUnauthorized, "invalid_signature", "signed URL expired or invalid")
		return
	}
	id := c.Param("id")
	if !h.deps.Storage.Verify(id, expires, c.Query("sig")) {
		writeError(c, http.StatusUnauthorized, "invalid_signature", "signed URL expired or invalid")
		return
	}
	a, err := h.deps.Assets.Get(c.Request.Context(), "default", id)
	if err != nil {
		writeErr(c, err)
		return
	}
	h.streamAsset(c, a)
}

func (h handler) thumbnail(c *gin.Context) {
	a, err := h.deps.Assets.Get(c.Request.Context(), Tenant(c), c.Param("id"))
	if err != nil {
		writeErr(c, err)
		return
	}
	w := queryInt(c, "width", 256)
	ht := queryInt(c, "height", 256)
	format := c.DefaultQuery("format", "webp")
	cacheFormat := format
	if cacheFormat == "" {
		cacheFormat = "webp"
	}
	if ok, err := h.deps.Storage.Exists(c.Request.Context(), blob.ThumbnailKey(a.ID, w, ht, cacheFormat)); err == nil && ok {
		h.deps.Metrics.AddThumbnailHit()
	}
	rc, contentType, err := h.deps.Pipeline.GenerateThumbnail(c.Request.Context(), a, w, ht, format)
	if err != nil {
		writeErr(c, err)
		return
	}
	defer func() { _ = rc.Close() }()
	c.DataFromReader(http.StatusOK, -1, contentType, rc, nil)
}

func (h handler) stats(c *gin.Context) {
	stats, err := h.assetStats(c.Request.Context(), Tenant(c))
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, stats)
}

func (h handler) bulkDelete(c *gin.Context) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "invalid json body")
		return
	}
	if len(req.IDs) > 100 {
		writeError(c, http.StatusBadRequest, "invalid_request", "ids limit is 100")
		return
	}
	results := make([]gin.H, 0, len(req.IDs))
	for _, id := range req.IDs {
		a, err := h.deps.Assets.Get(c.Request.Context(), Tenant(c), id)
		if err == nil {
			_ = h.deps.Storage.Delete(c.Request.Context(), a.StorageKey)
			_ = h.deps.Storage.DeleteRecursive(c.Request.Context(), "_thumbs/"+a.ID+"/")
			err = h.deps.Assets.Delete(c.Request.Context(), Tenant(c), id)
		}
		results = append(results, gin.H{"id": id, "deleted": err == nil, "error": errorString(err)})
	}
	h.invalidateStats(Tenant(c))
	c.JSON(http.StatusOK, gin.H{"object": "bulk_delete", "data": results})
}

func (h handler) createUpload(c *gin.Context) {
	if !h.acquireUpload(c) {
		return
	}
	defer h.releaseUpload()
	var req struct {
		Filename    string         `json:"filename"`
		Purpose     string         `json:"purpose"`
		ContentType string         `json:"content_type"`
		TotalBytes  int64          `json:"total_bytes"`
		Tags        []string       `json:"tags"`
		Metadata    map[string]any `json:"metadata"`
		Source      string         `json:"source"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "invalid json body")
		return
	}
	if !validPurposes[req.Purpose] {
		writeError(c, http.StatusBadRequest, "invalid_purpose", "invalid purpose")
		return
	}
	if req.TotalBytes > h.deps.Config.MaxUploadBytes {
		writeError(c, http.StatusRequestEntityTooLarge, "payload_too_large", "file too large")
		return
	}
	if req.TotalBytes > 0 {
		releaseQuota, ok := h.reserveQuota(c, req.TotalBytes)
		if !ok {
			return
		}
		releaseQuota()
	}
	now := time.Now().UTC()
	storageKey := storageKey(Tenant(c), req.Purpose, "asset-"+shortID(), req.Filename)
	providerUploadID := ""
	if h.deps.Storage.NativeMultipartSupported() {
		var err error
		providerUploadID, err = h.deps.Storage.CreateMultipartUpload(c.Request.Context(), storageKey, req.ContentType)
		if err != nil {
			writeErr(c, err)
			return
		}
	}
	sess := &store.UploadSession{
		ID: "upl-" + shortID(), TenantID: Tenant(c), Filename: req.Filename, Purpose: req.Purpose,
		ContentType: req.ContentType, TotalBytes: req.TotalBytes, ChunkSize: 10 * 1024 * 1024, StorageKey: storageKey,
		ProviderUploadID: providerUploadID, Status: "pending",
		Source: defaultString(req.Source, "upload"), Metadata: store.JSONMap(req.Metadata), TagNames: req.Tags, CreatedAt: now,
		ExpiresAt: now.Add(h.deps.Config.ChunkUploadMaxAge),
	}
	if err := h.deps.Uploads.CreateSession(c.Request.Context(), sess); err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"upload_id": sess.ID, "chunk_size": sess.ChunkSize, "expires_at": sess.ExpiresAt.Unix()})
}

func (h handler) putPart(c *gin.Context) {
	if !h.acquireUpload(c) {
		return
	}
	defer h.releaseUpload()
	sess, err := h.deps.Uploads.GetSession(c.Request.Context(), Tenant(c), c.Param("id"))
	if err != nil {
		writeErr(c, err)
		return
	}
	partNum, err := strconv.Atoi(c.Param("part"))
	if err != nil || partNum <= 0 {
		writeError(c, http.StatusBadRequest, "invalid_request", "invalid part number")
		return
	}
	if h.deps.Storage.NativeMultipartSupported() && sess.ProviderUploadID != "" {
		if c.Request.ContentLength > h.deps.Config.MaxUploadBytes && h.deps.Config.MaxUploadBytes > 0 {
			writeError(c, http.StatusRequestEntityTooLarge, "payload_too_large", "file too large")
			return
		}
		tmp, bytesWritten, _, _, err := spoolToTemp(c.Request.Body, h.deps.Config.MaxUploadBytes)
		if err != nil {
			if errors.Is(err, errPayloadTooLarge) {
				writeError(c, http.StatusRequestEntityTooLarge, "payload_too_large", "file too large")
				return
			}
			writeErr(c, err)
			return
		}
		defer func() {
			name := tmp.Name()
			_ = tmp.Close()
			_ = os.Remove(name)
		}()
		etag, bytesWritten, err := h.deps.Storage.UploadPart(c.Request.Context(), sess.StorageKey, sess.ProviderUploadID, partNum, tmp)
		if err != nil {
			if errors.Is(err, errPayloadTooLarge) {
				writeError(c, http.StatusRequestEntityTooLarge, "payload_too_large", "file too large")
				return
			}
			writeErr(c, err)
			return
		}
		h.deps.Metrics.AddUploadBytes(bytesWritten)
		if err := h.deps.Uploads.AddPart(c.Request.Context(), &store.UploadPart{UploadID: sess.ID, PartNum: partNum, ETag: etag, Bytes: bytesWritten}); err != nil {
			writeErr(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"part": partNum, "etag": etag})
		return
	}
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, h.deps.Config.MaxUploadBytes+1))
	if err != nil {
		writeErr(c, err)
		return
	}
	if int64(len(data)) > h.deps.Config.MaxUploadBytes {
		writeError(c, http.StatusRequestEntityTooLarge, "payload_too_large", "file too large")
		return
	}
	releaseQuota, ok := h.reserveQuota(c, int64(len(data)))
	if !ok {
		return
	}
	defer releaseQuota()
	etag := sha256Hex(data)
	if _, err := h.deps.Storage.Put(c.Request.Context(), blob.ChunkKey(sess.ID, partNum), bytes.NewReader(data)); err != nil {
		writeErr(c, err)
		return
	}
	h.deps.Metrics.AddUploadBytes(int64(len(data)))
	if err := h.deps.Uploads.AddPart(c.Request.Context(), &store.UploadPart{UploadID: sess.ID, PartNum: partNum, ETag: etag, Bytes: int64(len(data))}); err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"part": partNum, "etag": etag})
}

func (h handler) completeUpload(c *gin.Context) {
	if !h.acquireUpload(c) {
		return
	}
	defer h.releaseUpload()
	sess, err := h.deps.Uploads.GetSession(c.Request.Context(), Tenant(c), c.Param("id"))
	if err != nil {
		writeErr(c, err)
		return
	}
	var req struct {
		Parts []struct {
			Part int    `json:"part"`
			ETag string `json:"etag"`
		} `json:"parts"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "invalid json body")
		return
	}
	parts, err := h.deps.Uploads.ListParts(c.Request.Context(), sess.ID)
	if err != nil {
		writeErr(c, err)
		return
	}
	if len(parts) == 0 {
		writeError(c, http.StatusBadRequest, "invalid_request", "no uploaded parts")
		return
	}
	if h.deps.Storage.NativeMultipartSupported() && sess.ProviderUploadID != "" {
		var totalBytes int64
		nativeParts := make([]blob.MultipartPart, 0, len(parts))
		for _, p := range parts {
			totalBytes += p.Bytes
			nativeParts = append(nativeParts, blob.MultipartPart{PartNum: p.PartNum, ETag: p.ETag})
		}
		if sess.TotalBytes > 0 && totalBytes != sess.TotalBytes {
			writeError(c, http.StatusBadRequest, "invalid_request", "uploaded bytes do not match total_bytes")
			return
		}
		releaseQuota, ok := h.reserveQuota(c, totalBytes)
		if !ok {
			return
		}
		quotaCommitted := false
		defer func() {
			if !quotaCommitted {
				releaseQuota()
			}
		}()
		if err := h.deps.Storage.CompleteMultipartUpload(c.Request.Context(), sess.StorageKey, sess.ProviderUploadID, nativeParts); err != nil {
			writeErr(c, err)
			return
		}
		assetID := path.Base(path.Dir(sess.StorageKey))
		if !strings.HasPrefix(assetID, "asset-") {
			assetID = "asset-" + shortID()
		}
		asset := &store.Asset{
			ID: assetID, TenantID: Tenant(c), Purpose: sess.Purpose, Filename: sess.Filename, ContentType: sess.ContentType,
			Bytes: totalBytes, StorageKey: sess.StorageKey, Status: "uploaded", Source: sess.Source,
			Metadata: sess.Metadata, Tags: tagModels(sess.TagNames),
		}
		if err := h.deps.Assets.Create(c.Request.Context(), asset); err != nil {
			writeErr(c, err)
			return
		}
		c.Set(assetIDKey, asset.ID)
		h.invalidateStats(Tenant(c))
		quotaCommitted = true
		releaseQuota()
		_ = h.deps.Uploads.UpdateSessionStatus(c.Request.Context(), sess.ID, "completed")
		h.deps.Pipeline.Enqueue(context.Background(), asset)
		c.JSON(http.StatusOK, assetResponse(asset, false))
		return
	}
	tmp, err := os.CreateTemp("", "assethub-complete-*")
	if err != nil {
		writeErr(c, err)
		return
	}
	defer func() {
		name := tmp.Name()
		_ = tmp.Close()
		_ = os.Remove(name)
	}()
	hash := sha256.New()
	var totalBytes int64
	for _, p := range parts {
		rc, err := h.deps.Storage.Get(c.Request.Context(), blob.ChunkKey(sess.ID, p.PartNum))
		if err != nil {
			writeErr(c, err)
			return
		}
		n, copyErr := io.Copy(io.MultiWriter(tmp, hash), rc)
		closeErr := rc.Close()
		if copyErr != nil {
			writeErr(c, copyErr)
			return
		}
		if closeErr != nil {
			writeErr(c, closeErr)
			return
		}
		totalBytes += n
	}
	if sess.TotalBytes > 0 && totalBytes != sess.TotalBytes {
		writeError(c, http.StatusBadRequest, "invalid_request", "uploaded bytes do not match total_bytes")
		return
	}
	if totalBytes > h.deps.Config.MaxUploadBytes {
		writeError(c, http.StatusRequestEntityTooLarge, "payload_too_large", "file too large")
		return
	}
	releaseQuota, ok := h.reserveQuota(c, totalBytes)
	if !ok {
		return
	}
	quotaCommitted := false
	defer func() {
		if !quotaCommitted {
			releaseQuota()
		}
	}()
	id := "asset-" + shortID()
	key := storageKey(Tenant(c), sess.Purpose, id, sess.Filename)
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		writeErr(c, err)
		return
	}
	if _, err := h.deps.Storage.Put(c.Request.Context(), key, tmp); err != nil {
		writeErr(c, err)
		return
	}
	checksum := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	asset := &store.Asset{
		ID: id, TenantID: Tenant(c), Purpose: sess.Purpose, Filename: sess.Filename, ContentType: sess.ContentType,
		Bytes: totalBytes, StorageKey: key, Checksum: checksum, Status: "uploaded", Source: sess.Source,
		Metadata: sess.Metadata, Tags: tagModels(sess.TagNames),
	}
	if err := h.deps.Assets.Create(c.Request.Context(), asset); err != nil {
		writeErr(c, err)
		return
	}
	c.Set(assetIDKey, asset.ID)
	h.invalidateStats(Tenant(c))
	quotaCommitted = true
	releaseQuota()
	_ = h.deps.Uploads.UpdateSessionStatus(c.Request.Context(), sess.ID, "completed")
	_ = h.deps.Storage.DeleteRecursive(c.Request.Context(), blob.ChunkPrefix(sess.ID))
	h.deps.Pipeline.Enqueue(context.Background(), asset)
	c.JSON(http.StatusOK, assetResponse(asset, false))
}

func (h handler) cancelUpload(c *gin.Context) {
	id := c.Param("id")
	if sess, err := h.deps.Uploads.GetSession(c.Request.Context(), Tenant(c), id); err == nil && sess.ProviderUploadID != "" {
		_ = h.deps.Storage.AbortMultipartUpload(c.Request.Context(), sess.StorageKey, sess.ProviderUploadID)
	} else {
		_ = h.deps.Storage.DeleteRecursive(c.Request.Context(), blob.ChunkPrefix(id))
	}
	if err := h.deps.Uploads.DeleteSession(c.Request.Context(), id); err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "deleted": true})
}

func parseFilter(c *gin.Context) store.AssetFilter {
	return store.AssetFilter{
		Purpose: c.Query("purpose"), Status: c.Query("status"), Tags: parseTags(c.Query("tags")),
		Filename: c.Query("filename"), Source: c.Query("source"), ContentType: c.Query("content_type"),
		MetaModel: c.Query("meta_model"), MetaQuery: c.Query("meta_query"), Metadata: parseMetadataFilters(c),
		Limit:  queryInt(c, "limit", 20),
		Offset: queryInt(c, "offset", 0), Order: c.Query("order"), Cursor: c.Query("cursor"), After: c.Query("after"), Before: c.Query("before"),
	}
}

func parseMetadataFilters(c *gin.Context) []store.MetadataFilter {
	var out []store.MetadataFilter
	if key := strings.TrimSpace(c.Query("meta_key")); key != "" {
		mf := store.MetadataFilter{Key: key}
		if raw, ok := c.GetQuery("meta_value"); ok {
			mf.Value = parseMetadataQueryValue(raw)
			mf.HasValue = true
		}
		out = append(out, mf)
	}
	for key, raw := range c.QueryMap("metadata") {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out = append(out, store.MetadataFilter{Key: key, Value: parseMetadataQueryValue(raw), HasValue: true})
	}
	return out
}

func parseMetadataQueryValue(raw string) any {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err == nil {
		return v
	}
	return raw
}

func assetResponse(a *store.Asset, file bool) gin.H {
	tags := make([]string, 0, len(a.Tags))
	for _, t := range a.Tags {
		tags = append(tags, t.Name)
	}
	obj := "asset"
	if file {
		obj = "file"
	}
	out := gin.H{
		"id": a.ID, "object": obj, "bytes": a.Bytes, "created_at": a.CreatedAt.Unix(), "filename": a.Filename,
		"purpose": a.Purpose, "status": a.Status,
	}
	if !file {
		out["content_type"] = a.ContentType
		out["source"] = a.Source
		out["checksum"] = a.Checksum
		out["tags"] = tags
		out["metadata"] = a.Metadata
		out["updated_at"] = a.UpdatedAt.Unix()
		if a.ExpiresAt != nil {
			out["expires_at"] = a.ExpiresAt.Unix()
		} else {
			out["expires_at"] = nil
		}
	}
	return out
}

func storageKey(tenantID, purpose, id, filename string) string {
	filename = path.Base(filename)
	if filename == "." || filename == "/" {
		filename = "file"
	}
	return path.Join(tenantID, purpose, time.Now().UTC().Format("2006-01"), id, filename)
}

func shortID() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
}

func spoolToTemp(r io.Reader, maxBytes int64) (*os.File, int64, string, []byte, error) {
	tmp, err := os.CreateTemp("", "assethub-upload-*")
	if err != nil {
		return nil, 0, "", nil, err
	}
	ok := false
	defer func() {
		if !ok {
			name := tmp.Name()
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash), limitReader(r, maxBytes))
	if err != nil {
		return nil, 0, "", nil, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, 0, "", nil, err
	}
	sniff := make([]byte, 512)
	sn, err := io.ReadFull(tmp, sniff)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, 0, "", nil, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, 0, "", nil, err
	}
	ok = true
	return tmp, n, "sha256:" + hex.EncodeToString(hash.Sum(nil)), sniff[:sn], nil
}

func limitReader(r io.Reader, maxBytes int64) io.Reader {
	if maxBytes <= 0 {
		return r
	}
	return &limitedReader{r: r, remaining: maxBytes}
}

type limitedReader struct {
	r         io.Reader
	remaining int64
	exceeded  bool
}

func (r *limitedReader) Read(p []byte) (int, error) {
	if r.exceeded {
		return 0, errPayloadTooLarge
	}
	if r.remaining <= 0 {
		var one [1]byte
		n, err := r.r.Read(one[:])
		if n > 0 {
			r.exceeded = true
			return 0, errPayloadTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > r.remaining {
		p = p[:int(r.remaining)]
	}
	n, err := r.r.Read(p)
	r.remaining -= int64(n)
	if r.remaining == 0 && err == nil {
		var one [1]byte
		extra, extraErr := r.r.Read(one[:])
		if extra > 0 {
			r.exceeded = true
			return n, errPayloadTooLarge
		}
		if extraErr != nil && !errors.Is(extraErr, io.EOF) {
			return n, extraErr
		}
	}
	return n, err
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func parseTags(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func tagModels(tags []string) []store.Tag {
	out := make([]store.Tag, 0, len(tags))
	for _, t := range tags {
		out = append(out, store.Tag{Name: t})
	}
	return out
}

func parseJSONMap(raw string) store.JSONMap {
	if strings.TrimSpace(raw) == "" {
		return store.JSONMap{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return store.JSONMap{}
	}
	return store.JSONMap(m)
}

func parseExpiresIn(raw string) *time.Time {
	if raw == "" {
		return nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return nil
	}
	t := time.Now().UTC().Add(d)
	return &t
}

func queryInt(c *gin.Context, key string, fallback int) int {
	v, err := strconv.Atoi(c.Query(key))
	if err != nil {
		return fallback
	}
	return v
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (h handler) fetchExternal(ctx context.Context, raw string) ([]byte, string, error) {
	if err := h.validateExternalURL(raw); err != nil {
		return nil, "", err
	}
	client := &http.Client{
		Timeout: h.deps.Config.ExternalFetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			return validateExternalHost(req.URL.Hostname())
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				if err := validateExternalHost(host); err != nil {
					return nil, err
				}
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("external URL returned %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, h.deps.Config.ExternalFetchMaxSize+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > h.deps.Config.ExternalFetchMaxSize {
		return nil, "", fmt.Errorf("external file too large")
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = http.DetectContentType(data)
	}
	return data, ct, nil
}

func (h handler) validateExternalURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("only http and https URLs are allowed")
	}
	return validateExternalHost(u.Hostname())
}

func validatePublicHost(host string) error {
	ips, err := net.LookupIP(host)
	if err != nil {
		return err
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("private network URLs are not allowed")
		}
	}
	return nil
}
