package workspaceapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/saker-ai/filehub/pkg/workspace"
)

// --- DTOs ------------------------------------------------------------------

type workspaceResponse struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Sequence    int64  `json:"sequence"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
	DeletedAt   *int64 `json:"deleted_at,omitempty"`
}

func workspaceJSON(ws *workspace.Workspace) workspaceResponse {
	out := workspaceResponse{
		ID:          ws.ID,
		Object:      "workspace",
		Name:        ws.Name,
		Description: ws.Description,
		Sequence:    ws.Sequence,
		CreatedAt:   ws.CreatedAt.Unix(),
		UpdatedAt:   ws.UpdatedAt.Unix(),
	}
	if ws.DeletedAt != nil {
		v := ws.DeletedAt.Unix()
		out.DeletedAt = &v
	}
	return out
}

type revisionJSON struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	AssetID   string `json:"asset_id,omitempty"`
	Bytes     int64  `json:"bytes,omitempty"`
	Checksum  string `json:"checksum,omitempty"`
	Mode      uint32 `json:"mode,omitempty"`
	ActorID   string `json:"actor_id,omitempty"`
	DeviceID  string `json:"device_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Note      string `json:"note,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

func revisionJSONOf(rev *workspace.Revision) *revisionJSON {
	if rev == nil {
		return nil
	}
	return &revisionJSON{
		ID:        rev.ID,
		Kind:      rev.Kind,
		AssetID:   rev.AssetID,
		Bytes:     rev.Bytes,
		Checksum:  rev.Checksum,
		Mode:      rev.Mode,
		ActorID:   rev.ActorID,
		DeviceID:  rev.DeviceID,
		SessionID: rev.SessionID,
		Note:      rev.Note,
		CreatedAt: rev.CreatedAt.Unix(),
	}
}

func writeList(c *gin.Context, data any, hasMore bool, nextCursor string, extra map[string]any) {
	body := gin.H{"object": "list", "data": data, "has_more": hasMore, "next_cursor": nextCursor}
	for k, v := range extra {
		body[k] = v
	}
	c.JSON(http.StatusOK, body)
}

// --- Workspace management (doc §8.1) ----------------------------------------

func (h handler) createWorkspace(c *gin.Context) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := bindJSONLimited(c, &req, int64(h.svc().Limits().MaxNoteBytes)*4); err != nil {
		return
	}
	ws, err := h.svc().CreateWorkspace(c.Request.Context(), tenant(c), strings.TrimSpace(req.Name), req.Description)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, workspaceJSON(ws))
}

func (h handler) listWorkspaces(c *gin.Context) {
	limit := workspace.ClampListLimit(queryInt(c, "limit", workspace.DefaultListLimit))
	items, hasMore, err := h.svc().ListWorkspaces(c.Request.Context(), tenant(c), c.Query("cursor"), limit)
	if err != nil {
		writeErr(c, err)
		return
	}
	data := make([]workspaceResponse, 0, len(items))
	for _, ws := range items {
		data = append(data, workspaceJSON(ws))
	}
	cursor := ""
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		cursor = timeIDCursor(last.CreatedAt, last.ID)
	}
	writeList(c, data, hasMore, cursor, nil)
}

func (h handler) getWorkspace(c *gin.Context) {
	ws, err := h.svc().GetWorkspace(c.Request.Context(), tenant(c), c.Param("id"))
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, workspaceJSON(ws))
}

func (h handler) patchWorkspace(c *gin.Context) {
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := bindJSONLimited(c, &req, int64(h.svc().Limits().MaxNoteBytes)*4); err != nil {
		return
	}
	ws, err := h.svc().PatchWorkspace(c.Request.Context(), tenant(c), c.Param("id"), req.Name, req.Description)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, workspaceJSON(ws))
}

func (h handler) deleteWorkspace(c *gin.Context) {
	if err := h.svc().DeleteWorkspace(c.Request.Context(), tenant(c), c.Param("id")); err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": c.Param("id"), "object": "workspace", "deleted": true})
}

// --- Browsing and history (doc §8.2 / §8.5) ---------------------------------

func (h handler) tree(c *gin.Context) {
	limit := workspace.ClampListLimit(queryInt(c, "limit", workspace.DefaultListLimit))
	nodes, hasMore, err := h.svc().Tree(c.Request.Context(), tenant(c), c.Param("id"), c.Query("prefix"), c.Query("cursor"), limit)
	if err != nil {
		writeErr(c, err)
		return
	}
	data := make([]gin.H, 0, len(nodes))
	for _, n := range nodes {
		data = append(data, gin.H{
			"path": n.Path,
			"revision": gin.H{
				"id":         n.RevisionID,
				"kind":       n.Kind,
				"asset_id":   n.AssetID,
				"bytes":      n.Bytes,
				"checksum":   n.Checksum,
				"mode":       n.Mode,
				"created_at": n.UpdatedAt.Unix(),
			},
		})
	}
	cursor := ""
	if hasMore && len(nodes) > 0 {
		cursor = nodes[len(nodes)-1].Path
	}
	writeList(c, data, hasMore, cursor, nil)
}

func (h handler) getEntry(c *gin.Context) {
	p := c.Query("path")
	view, err := h.svc().GetEntry(c.Request.Context(), tenant(c), c.Param("id"), p)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"path": view.Path, "revision": revisionJSONOf(view.Revision)})
}

func (h handler) history(c *gin.Context) {
	limit := workspace.ClampListLimit(queryInt(c, "limit", workspace.DefaultListLimit))
	revisions, hasMore, err := h.svc().History(c.Request.Context(), tenant(c), c.Param("id"), c.Query("path"), c.Query("cursor"), limit)
	if err != nil {
		writeErr(c, err)
		return
	}
	data := make([]*revisionJSON, 0, len(revisions))
	for _, rev := range revisions {
		data = append(data, revisionJSONOf(rev))
	}
	cursor := ""
	if hasMore && len(revisions) > 0 {
		last := revisions[len(revisions)-1]
		cursor = timeIDCursor(last.CreatedAt, last.ID)
	}
	writeList(c, data, hasMore, cursor, nil)
}

func (h handler) changes(c *gin.Context) {
	limit := workspace.ClampListLimit(queryInt(c, "limit", workspace.DefaultListLimit))
	after := queryInt64(c, "after", 0)
	views, next, hasMore, err := h.svc().ListChanges(c.Request.Context(), tenant(c), c.Param("id"), after, limit)
	if err != nil {
		writeErr(c, err)
		return
	}
	data := make([]gin.H, 0, len(views))
	for _, v := range views {
		rev := gin.H{}
		if v.Revision != nil {
			rev = gin.H{
				"id":       v.Revision.ID,
				"asset_id": v.Revision.AssetID,
				"bytes":    v.Revision.Bytes,
				"checksum": v.Revision.Checksum,
				"mode":     v.Revision.Mode,
			}
		}
		data = append(data, gin.H{"sequence": v.Sequence, "path": v.Path, "kind": v.Kind, "revision": rev})
	}
	writeList(c, data, hasMore, "", gin.H{"next_sequence": next})
}

func (h handler) restore(c *gin.Context) {
	var req struct {
		Path       string `json:"path"`
		RevisionID string `json:"revision_id"`
		Note       string `json:"note"`
	}
	if err := bindJSONLimited(c, &req, int64(h.svc().Limits().MaxNoteBytes)*4); err != nil {
		return
	}
	result, err := h.svc().Restore(c.Request.Context(), tenant(c), c.Param("id"), req.Path, req.RevisionID, req.Note, actor(c), "", "")
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"object": "restore", "path": result.Path, "revision_id": result.RevisionID,
		"asset_id": result.AssetID, "bytes": result.Bytes, "sequence": result.Sequence,
	})
}

// --- Commits (doc §8.3) ------------------------------------------------------

type commitOperationDTO struct {
	Kind           string `json:"kind"`
	Path           string `json:"path"`
	AssetID        string `json:"asset_id"`
	BaseRevisionID string `json:"base_revision_id"`
	Mode           uint32 `json:"mode"`
}

type commitRequestDTO struct {
	DeviceID   string               `json:"device_id"`
	SessionID  string               `json:"session_id"`
	Note       string               `json:"note"`
	Operations []commitOperationDTO `json:"operations"`
}

func (h handler) commit(c *gin.Context) {
	requestID := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if requestID == "" {
		writeError(c, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key header is required")
		return
	}
	maxBody := h.svc().Limits().MaxCommitBodyBytes
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBody+1))
	if err != nil {
		writeErr(c, workspace.ErrPayloadTooLarge)
		return
	}
	if int64(len(body)) > maxBody {
		h.metrics.RecordWorkspaceCommit("rejected")
		writeErr(c, workspace.ErrPayloadTooLarge)
		return
	}
	var dto commitRequestDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "invalid json body")
		return
	}
	req := workspace.CommitRequest{
		DeviceID:   dto.DeviceID,
		SessionID:  dto.SessionID,
		Note:       dto.Note,
		RequestID:  requestID,
		Operations: make([]workspace.Operation, 0, len(dto.Operations)),
	}
	for _, op := range dto.Operations {
		req.Operations = append(req.Operations, workspace.Operation{
			Kind: op.Kind, Path: op.Path, AssetID: op.AssetID,
			BaseRevisionID: op.BaseRevisionID, Mode: op.Mode,
		})
	}
	result, err := h.svc().Commit(c.Request.Context(), tenant(c), c.Param("id"), req, actor(c))
	if err != nil {
		h.metrics.RecordWorkspaceCommit("error")
		writeErr(c, err)
		return
	}
	h.metrics.RecordWorkspaceCommit("ok")
	for _, op := range result.Results {
		h.metrics.RecordWorkspaceOperation(op.Kind, op.Resolution)
		if op.Resolution == workspace.ResolutionConflict {
			h.metrics.RecordWorkspaceConflict()
		}
	}
	if result.Response != nil {
		c.Data(http.StatusOK, "application/json; charset=utf-8", result.Response)
		return
	}
	c.JSON(http.StatusOK, result)
}

// --- Shares (doc §8.4 / §8.5) --------------------------------------------------

func (h handler) createShare(c *gin.Context) {
	var req struct {
		Path      string `json:"path"`
		ExpiresIn string `json:"expires_in"`
	}
	if err := bindJSONLimited(c, &req, int64(h.svc().Limits().MaxNoteBytes)*4); err != nil {
		return
	}
	var ttl time.Duration
	if strings.TrimSpace(req.ExpiresIn) != "" {
		parsed, err := time.ParseDuration(req.ExpiresIn)
		if err != nil || parsed <= 0 {
			writeError(c, http.StatusBadRequest, "invalid_request", "invalid expires_in duration")
			return
		}
		ttl = parsed
	}
	share, token, err := h.svc().CreateShare(c.Request.Context(), tenant(c), c.Param("id"), req.Path, actor(c), ttl)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"object":     "share",
		"id":         share.ID,
		"path":       share.Path,
		"token":      token,
		"url":        "/s/" + token,
		"expires_at": unixOrNil(share.ExpiresAt),
		"created_at": share.CreatedAt.Unix(),
	})
}

func (h handler) listShares(c *gin.Context) {
	limit := workspace.ClampListLimit(queryInt(c, "limit", workspace.DefaultListLimit))
	offset := queryInt(c, "offset", 0)
	shares, hasMore, err := h.svc().ListShares(c.Request.Context(), tenant(c), c.Param("id"), offset, limit)
	if err != nil {
		writeErr(c, err)
		return
	}
	data := make([]gin.H, 0, len(shares))
	for _, s := range shares {
		data = append(data, gin.H{
			"object":     "share",
			"id":         s.ID,
			"path":       s.Path,
			"token_hint": s.TokenPrefix,
			"creator_id": s.CreatorID,
			"expires_at": unixOrNil(s.ExpiresAt),
			"revoked_at": unixOrNil(s.RevokedAt),
			"created_at": s.CreatedAt.Unix(),
		})
	}
	writeList(c, data, hasMore, "", nil)
}

func (h handler) revokeShare(c *gin.Context) {
	if err := h.svc().RevokeShare(c.Request.Context(), tenant(c), c.Param("id"), c.Param("share_id")); err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": c.Param("share_id"), "object": "share", "revoked": true})
}

// safeInlineTypes are the only content types served inline on /s/:token.
// Everything else (including HTML and SVG) is always served as an
// attachment, so shared content can never execute in the FileHub origin.
var safeInlineTypes = map[string]bool{
	"text/plain":      true,
	"application/pdf": true,
	"image/png":       true,
	"image/jpeg":      true,
	"image/gif":       true,
	"image/webp":      true,
}

func (h handler) publicShare(c *gin.Context) {
	// Anonymous endpoint: never reuse authenticated request context values.
	resolution, err := h.svc().ResolveShare(c.Request.Context(), c.Param("token"))
	if err != nil {
		if errors.Is(err, workspace.ErrGone) {
			writeError(c, http.StatusGone, "workspace_deleted", "workspace has been deleted")
			return
		}
		writeError(c, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if h.deps.Assets == nil || h.deps.Storage == nil {
		writeError(c, http.StatusInternalServerError, "not_configured", "share content is not configured")
		return
	}
	asset, err := h.deps.Assets.Get(c.Request.Context(), resolution.TenantID, resolution.AssetID)
	if err != nil {
		writeError(c, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	rc, err := h.deps.Storage.Get(c.Request.Context(), asset.StorageKey)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	defer func() { _ = rc.Close() }()

	h.metrics.RecordWorkspaceReadEvent(workspace.ReadKindShare)

	contentType := asset.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	// Defense-in-depth headers (doc §8.4/§8.5, SEC-05): sandboxed CSP, no
	// sniffing, no caching, attachment disposition except for safe inline
	// requests.
	c.Header("Content-Security-Policy", "sandbox")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "no-store")
	c.Header("Cross-Origin-Resource-Policy", "cross-origin")
	disposition := "attachment"
	if c.Query("inline") == "1" && safeInlineTypes[mediaTypeOnly(contentType)] {
		disposition = "inline"
	}
	filename := asset.Filename
	if filename == "" {
		filename = "shared-file"
	}
	c.Header("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": filename}))
	c.Header("Content-Type", contentType)
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, io.LimitReader(rc, asset.Bytes+1))
}

// --- Read events (doc §8.4 / §8.5) ----------------------------------------------

func (h handler) recordReadEvents(c *gin.Context) {
	var req struct {
		Events []struct {
			Path      string `json:"path"`
			Kind      string `json:"kind"`
			SessionID string `json:"session_id"`
			DeviceID  string `json:"device_id"`
			Count     int64  `json:"count"`
		} `json:"events"`
	}
	if err := bindJSONLimited(c, &req, int64(h.svc().Limits().MaxCommitBodyBytes)); err != nil {
		return
	}
	inputs := make([]workspace.ReadEventInput, 0, len(req.Events))
	for _, ev := range req.Events {
		inputs = append(inputs, workspace.ReadEventInput{
			Path: ev.Path, Kind: ev.Kind, SessionID: ev.SessionID, DeviceID: ev.DeviceID, Count: ev.Count,
		})
	}
	stats, err := h.svc().RecordReadEvents(c.Request.Context(), tenant(c), c.Param("id"), actor(c), inputs)
	if err != nil {
		writeErr(c, err)
		return
	}
	for _, in := range inputs {
		h.metrics.RecordWorkspaceReadEvent(in.Kind)
	}
	data := make([]gin.H, 0, len(stats))
	for _, s := range stats {
		data = append(data, gin.H{"path": s.Path, "count": s.Count})
	}
	c.JSON(http.StatusOK, gin.H{"object": "read_events", "recorded": len(inputs), "totals": data})
}

func (h handler) readStats(c *gin.Context) {
	days := queryInt(c, "days", 30)
	stats, err := h.svc().ReadStats(c.Request.Context(), tenant(c), c.Param("id"), c.Query("prefix"), days)
	if err != nil {
		writeErr(c, err)
		return
	}
	data := make([]gin.H, 0, len(stats))
	for _, s := range stats {
		data = append(data, gin.H{"path": s.Path, "day": s.Day, "count": s.Count})
	}
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": data})
}

// --- helpers ------------------------------------------------------------------

// bindJSONLimited decodes a JSON body with an explicit byte cap.
func bindJSONLimited(c *gin.Context, dst any, maxBytes int64) error {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBytes+1))
	if err != nil || int64(len(body)) > maxBytes {
		writeError(c, http.StatusRequestEntityTooLarge, "payload_too_large", "request body too large")
		return errors.New("payload too large")
	}
	if err := json.Unmarshal(body, dst); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "invalid json body")
		return err
	}
	return nil
}

func unixOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}

func mediaTypeOnly(contentType string) string {
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = contentType[:i]
	}
	return strings.ToLower(strings.TrimSpace(contentType))
}

// timeIDCursor mirrors gormrepo's newest-first cursor shape.
func timeIDCursor(createdAt time.Time, id string) string {
	return strconv.FormatInt(createdAt.UnixNano(), 10) + ":" + id
}
