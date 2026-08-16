// Package workspaceapi is the HTTP transport for the FileHub Workspace
// domain (doc §8). Handlers are gin-based like the rest of FileHub; the
// OpenAPI surface is registered docs-only into the shared huma API (doc §9,
// R-13).
package workspaceapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/saker-ai/filehub/pkg/storage"
	"github.com/saker-ai/filehub/pkg/store"
	"github.com/saker-ai/filehub/pkg/workspace"
)

const (
	// tenantKey mirrors the context key used by pkg/api Auth middleware.
	// Kept as a literal to avoid importing pkg/api (import cycle: pkg/api
	// mounts this package).
	tenantKey = "tenant_id"
	// actorKey carries the authenticated principal id when available.
	actorKey = "workspace_actor_id"
)

// Metrics is the counter surface workspaceapi reports into. pkg/api.Metrics
// implements it; nil falls back to a no-op implementation.
type Metrics interface {
	RecordWorkspaceCommit(outcome string)
	RecordWorkspaceOperation(kind, resolution string)
	RecordWorkspaceConflict()
	RecordWorkspaceReadEvent(kind string)
	AddWorkspaceSyncBytes(direction string, n int64)
}

type noopMetrics struct{}

func (noopMetrics) RecordWorkspaceCommit(string)            {}
func (noopMetrics) RecordWorkspaceOperation(string, string) {}
func (noopMetrics) RecordWorkspaceConflict()                {}
func (noopMetrics) RecordWorkspaceReadEvent(string)         {}
func (noopMetrics) AddWorkspaceSyncBytes(string, int64)     {}

// Deps carries everything the workspace routes need. All fields except
// Service are optional; a nil Service means the routes are not mounted at
// all (doc §13).
type Deps struct {
	Service *workspace.Service
	// Assets resolves share content; required for /s/:token.
	Assets store.AssetRepo
	// Storage streams share content; required for /s/:token.
	Storage *storage.Store
	Metrics Metrics
}

// Register mounts the workspace API. v1 receives the authenticated
// /v1/workspaces routes; engine receives the anonymous /s/:token share
// route. Must only be called when deps.Service != nil.
func Register(v1 *gin.RouterGroup, engine *gin.Engine, deps Deps) {
	h := handler{deps: deps, metrics: deps.Metrics}
	if h.metrics == nil {
		h.metrics = noopMetrics{}
	}

	ws := v1.Group("/workspaces")
	ws.POST("", h.createWorkspace)
	ws.GET("", h.listWorkspaces)
	ws.GET("/:id", h.getWorkspace)
	ws.PATCH("/:id", h.patchWorkspace)
	ws.DELETE("/:id", h.deleteWorkspace)
	ws.GET("/:id/tree", h.tree)
	ws.GET("/:id/entries", h.getEntry)
	ws.GET("/:id/history", h.history)
	ws.GET("/:id/changes", h.changes)
	ws.POST("/:id/restore", h.restore)
	ws.POST("/:id/commits", h.commit)
	ws.POST("/:id/shares", h.createShare)
	ws.GET("/:id/shares", h.listShares)
	ws.DELETE("/:id/shares/:share_id", h.revokeShare)
	ws.POST("/:id/read-events", h.recordReadEvents)
	ws.GET("/:id/read-stats", h.readStats)

	engine.GET("/s/:token", h.publicShare)
}

type handler struct {
	deps    Deps
	metrics Metrics
}

func (h handler) svc() *workspace.Service { return h.deps.Service }

// tenant resolves the authenticated tenant exactly like pkg/api.
func tenant(c *gin.Context) string {
	if v, ok := c.Get(tenantKey); ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return "default"
}

// actor resolves the authenticated principal id recorded by the Auth
// middleware ("" for API-key authentication).
func actor(c *gin.Context) string {
	if v, ok := c.Get(actorKey); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func writeError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"error": gin.H{
		"message":    message,
		"type":       errorType(status),
		"code":       code,
		"request_id": c.Writer.Header().Get("X-Request-ID"),
	}})
}

func errorType(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return "authentication_error"
	case status >= 500:
		return "server_error"
	default:
		return "invalid_request_error"
	}
}

// writeErr maps workspace sentinel errors to HTTP responses (doc §8).
func writeErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, workspace.ErrNotFound):
		writeError(c, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, workspace.ErrGone):
		writeError(c, http.StatusGone, "workspace_deleted", "workspace has been deleted")
	case errors.Is(err, workspace.ErrConflictDigest):
		writeError(c, http.StatusConflict, "idempotency_conflict", "idempotency key reused with a different request")
	case errors.Is(err, workspace.ErrInvalidPath):
		writeError(c, http.StatusUnprocessableEntity, "invalid_path", err.Error())
	case errors.Is(err, workspace.ErrExcludedPath):
		writeError(c, http.StatusUnprocessableEntity, "excluded_path", err.Error())
	case errors.Is(err, workspace.ErrInvalidOperation):
		writeError(c, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, workspace.ErrInvalidAsset):
		writeError(c, http.StatusUnprocessableEntity, "invalid_asset", err.Error())
	case errors.Is(err, workspace.ErrLimitExceeded):
		writeError(c, http.StatusUnprocessableEntity, "limit_exceeded", err.Error())
	case errors.Is(err, workspace.ErrPayloadTooLarge):
		writeError(c, http.StatusRequestEntityTooLarge, "payload_too_large", err.Error())
	case errors.Is(err, workspace.ErrInvalidShareToken):
		writeError(c, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, store.ErrNotFound):
		writeError(c, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, store.ErrConflict):
		writeError(c, http.StatusConflict, "conflict", "resource conflict")
	default:
		writeError(c, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

func queryInt(c *gin.Context, key string, fallback int) int {
	var out int
	if _, err := parseIntFromQuery(c, key, &out); err != nil {
		return fallback
	}
	return out
}

func parseIntFromQuery(c *gin.Context, key string, out *int) (bool, error) {
	raw := c.Query(key)
	if raw == "" {
		return false, nil
	}
	n := 0
	for _, r := range raw {
		if r < '0' || r > '9' {
			return false, errors.New("not an integer")
		}
		n = n*10 + int(r-'0')
		if n > 100000000 {
			return false, errors.New("integer overflow")
		}
	}
	*out = n
	return true, nil
}

func queryInt64(c *gin.Context, key string, fallback int64) int64 {
	raw := c.Query(key)
	if raw == "" {
		return fallback
	}
	var n int64
	for _, r := range raw {
		if r < '0' || r > '9' {
			return fallback
		}
		n = n*10 + int64(r-'0')
		if n > 9007199254740992 {
			return fallback
		}
	}
	return n
}
