package workspaceapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// Doc input/output shapes for the docs-only OpenAPI registration. They
// mirror the JSON contracts of doc §8.5; runtime handling stays in gin.

type wsIDInput struct {
	ID string `path:"id" doc:"Workspace ID" required:"true" example:"ws-abc123"`
}

type wsCreateInput struct {
	Body struct {
		Name        string `json:"name" doc:"Workspace name" required:"true"`
		Description string `json:"description" doc:"Optional description"`
	}
}

type wsListInput struct {
	Cursor string `query:"cursor" doc:"Opaque pagination cursor"`
	Limit  int    `query:"limit" doc:"Page size (default 100, max 500)" default:"100"`
}

type wsPatchInput struct {
	ID   string `path:"id" required:"true"`
	Body struct {
		Name        *string `json:"name" doc:"New name"`
		Description *string `json:"description" doc:"New description"`
	}
}

type wsTreeInput struct {
	ID     string `path:"id" required:"true"`
	Prefix string `query:"prefix" doc:"Path prefix filter"`
	Cursor string `query:"cursor" doc:"Last path of previous page"`
	Limit  int    `query:"limit" default:"100"`
}

type wsEntryInput struct {
	ID   string `path:"id" required:"true"`
	Path string `query:"path" doc:"Exact entry path" required:"true"`
}

type wsHistoryInput struct {
	ID     string `path:"id" required:"true"`
	Path   string `query:"path" doc:"Restrict to one path"`
	Cursor string `query:"cursor"`
	Limit  int    `query:"limit" default:"100"`
}

type wsChangesInput struct {
	ID    string `path:"id" required:"true"`
	After int64  `query:"after" doc:"Return sequences strictly greater than this" default:"0"`
	Limit int    `query:"limit" default:"100"`
}

type wsRestoreInput struct {
	ID   string `path:"id" required:"true"`
	Body struct {
		Path       string `json:"path" required:"true"`
		RevisionID string `json:"revision_id" required:"true"`
		Note       string `json:"note"`
	}
}

type wsCommitOperation struct {
	Kind           string `json:"kind" doc:"put or delete" required:"true"`
	Path           string `json:"path" required:"true"`
	AssetID        string `json:"asset_id" doc:"Required for put"`
	BaseRevisionID string `json:"base_revision_id" doc:"Client-visible current revision"`
	Mode           uint32 `json:"mode" doc:"Unix permission bits"`
}

type wsCommitInput struct {
	ID             string `path:"id" required:"true"`
	IdempotencyKey string `header:"Idempotency-Key" doc:"Stable client request ID" required:"true"`
	Body           struct {
		DeviceID   string              `json:"device_id" required:"true"`
		SessionID  string              `json:"session_id"`
		Note       string              `json:"note"`
		Operations []wsCommitOperation `json:"operations"`
	}
}

type wsShareCreateInput struct {
	ID   string `path:"id" required:"true"`
	Body struct {
		Path      string `json:"path" required:"true"`
		ExpiresIn string `json:"expires_in" doc:"Go duration, e.g. 24h"`
	}
}

type wsShareListInput struct {
	ID     string `path:"id" required:"true"`
	Offset int    `query:"offset" default:"0"`
	Limit  int    `query:"limit" default:"100"`
}

type wsShareRevokeInput struct {
	ID      string `path:"id" required:"true"`
	ShareID string `path:"share_id" required:"true"`
}

type wsReadEvent struct {
	Path      string `json:"path" required:"true"`
	Kind      string `json:"kind" doc:"human, agent or share" required:"true"`
	SessionID string `json:"session_id"`
	DeviceID  string `json:"device_id"`
	Count     int64  `json:"count" default:"1"`
}

type wsReadEventsInput struct {
	ID   string `path:"id" required:"true"`
	Body struct {
		Events []wsReadEvent `json:"events"`
	}
}

type wsReadStatsInput struct {
	ID     string `path:"id" required:"true"`
	Prefix string `query:"prefix"`
	Days   int    `query:"days" default:"30"`
}

type wsShareTokenInput struct {
	Token string `path:"token" required:"true"`
}

type wsOutput struct{}

// registerDoc mirrors the docs-only registration helper in pkg/api. The
// handler returns nil: these operations document the contract without
// creating runtime huma handlers (doc §9, R-13).
func registerDoc[I, O any](api huma.API, method, path, operationID, summary string, security []map[string][]string) {
	huma.Register(api, huma.Operation{
		OperationID: operationID,
		Method:      method,
		Path:        path,
		Tags:        []string{"Workspaces"},
		Summary:     summary,
		Security:    security,
		Errors:      []int{400, 401, 403, 404, 409, 410, 413, 422, 429, 500},
	}, func(ctx context.Context, input *I) (*O, error) { return nil, nil })
}

// RegisterDocs registers workspace operations into the shared docs-only
// huma API (doc §9, R-13).
func RegisterDocs(api huma.API) {
	security := []map[string][]string{{"BearerAuth": {}}, {"APIKeyAuth": {}}}

	registerDoc[struct{}, wsOutput](api, http.MethodPost, "/v1/workspaces", "create-workspace", "Create a workspace", security)
	registerDoc[wsListInput, wsOutput](api, http.MethodGet, "/v1/workspaces", "list-workspaces", "List workspaces", security)
	registerDoc[wsIDInput, wsOutput](api, http.MethodGet, "/v1/workspaces/{id}", "get-workspace", "Get a workspace", security)
	registerDoc[wsPatchInput, wsOutput](api, http.MethodPatch, "/v1/workspaces/{id}", "patch-workspace", "Update workspace name or description", security)
	registerDoc[wsIDInput, wsOutput](api, http.MethodDelete, "/v1/workspaces/{id}", "delete-workspace", "Soft-delete a workspace", security)
	registerDoc[wsTreeInput, wsOutput](api, http.MethodGet, "/v1/workspaces/{id}/tree", "workspace-tree", "List workspace tree entries", security)
	registerDoc[wsEntryInput, wsOutput](api, http.MethodGet, "/v1/workspaces/{id}/entries", "workspace-entry", "Get one workspace entry", security)
	registerDoc[wsHistoryInput, wsOutput](api, http.MethodGet, "/v1/workspaces/{id}/history", "workspace-history", "List revision history", security)
	registerDoc[wsChangesInput, wsOutput](api, http.MethodGet, "/v1/workspaces/{id}/changes", "workspace-changes", "List incremental changes", security)
	registerDoc[wsRestoreInput, wsOutput](api, http.MethodPost, "/v1/workspaces/{id}/restore", "workspace-restore", "Restore a historical revision", security)
	registerDoc[wsCommitInput, wsOutput](api, http.MethodPost, "/v1/workspaces/{id}/commits", "workspace-commit", "Atomically commit operations", security)
	registerDoc[wsShareCreateInput, wsOutput](api, http.MethodPost, "/v1/workspaces/{id}/shares", "workspace-create-share", "Create a public share link", security)
	registerDoc[wsShareListInput, wsOutput](api, http.MethodGet, "/v1/workspaces/{id}/shares", "workspace-list-shares", "List share links", security)
	registerDoc[wsShareRevokeInput, wsOutput](api, http.MethodDelete, "/v1/workspaces/{id}/shares/{share_id}", "workspace-revoke-share", "Revoke a share link", security)
	registerDoc[wsReadEventsInput, wsOutput](api, http.MethodPost, "/v1/workspaces/{id}/read-events", "workspace-read-events", "Record read events", security)
	registerDoc[wsReadStatsInput, wsOutput](api, http.MethodGet, "/v1/workspaces/{id}/read-stats", "workspace-read-stats", "Read statistics", security)
	registerDoc[wsShareTokenInput, wsOutput](api, http.MethodGet, "/s/{token}", "workspace-public-share", "Resolve a public share token (anonymous)", nil)
}
