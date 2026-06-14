package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

type docIDInput struct {
	ID string `path:"id" doc:"Asset or file ID" required:"true" example:"asset-abc123"`
}

type docUploadIDInput struct {
	ID string `path:"id" doc:"Upload session ID" required:"true" example:"upl-abc123"`
}

type docPartInput struct {
	ID   string `path:"id" doc:"Upload session ID" required:"true" example:"upl-abc123"`
	Part int    `path:"part" doc:"Part number" required:"true" example:"1"`
}

type docListAssetsInput struct {
	Purpose     string `query:"purpose" doc:"Filter by purpose" example:"media"`
	Status      string `query:"status" doc:"Filter by status" example:"ready"`
	Tags        string `query:"tags" doc:"Comma-separated tag filter using AND semantics" example:"ai-generated,approved"`
	Filename    string `query:"filename" doc:"Filename fuzzy search" example:"portrait"`
	Source      string `query:"source" doc:"Filter by source" example:"ai-generated"`
	ContentType string `query:"content_type" doc:"Filter by MIME type or prefix" example:"image/"`
	MetaModel   string `query:"meta_model" doc:"Filter metadata.model exactly" example:"flux"`
	MetaQuery   string `query:"meta_query" doc:"Metadata JSON LIKE search" example:"workflow_id"`
	MetaKey     string `query:"meta_key" doc:"Filter assets containing a metadata key, or pair with meta_value for exact matching" example:"workflow_id"`
	MetaValue   string `query:"meta_value" doc:"Exact metadata value for meta_key. JSON scalars are supported, for example true or 123" example:"abc123"`
	Limit       int    `query:"limit" doc:"Maximum results" default:"20" example:"20"`
	Offset      int    `query:"offset" doc:"Offset pagination" default:"0" example:"0"`
	Order       string `query:"order" doc:"Sort order asc or desc" example:"desc"`
	After       string `query:"after" doc:"Cursor after ID"`
	Before      string `query:"before" doc:"Cursor before ID"`
}

type docListFilesInput struct {
	Purpose string `query:"purpose" doc:"Filter by purpose" example:"assistants"`
	Limit   int    `query:"limit" doc:"Maximum results" default:"20" example:"20"`
	Order   string `query:"order" doc:"Sort order asc or desc" example:"desc"`
	After   string `query:"after" doc:"Cursor after ID"`
}

type docDownloadInput struct {
	ID       string `path:"id" doc:"Asset or file ID" required:"true"`
	Download bool   `query:"download" doc:"Use attachment content disposition"`
}

type docThumbnailInput struct {
	ID     string `path:"id" doc:"Asset ID" required:"true"`
	Width  int    `query:"width" doc:"Thumbnail width" default:"256"`
	Height int    `query:"height" doc:"Thumbnail height" default:"256"`
	Format string `query:"format" doc:"Thumbnail format" default:"jpg"`
}

type docSignedDownloadInput struct {
	ID      string `path:"id" doc:"Asset ID" required:"true"`
	Expires int64  `query:"expires" doc:"Unix expiration timestamp" required:"true"`
	Sig     string `query:"sig" doc:"HMAC signature" required:"true"`
}

type docAssetOutput struct {
	Body map[string]any `json:"body"`
}

type docListOutput struct {
	Body struct {
		Object  string           `json:"object" example:"list"`
		Data    []map[string]any `json:"data"`
		HasMore bool             `json:"has_more"`
	}
}

type docStatsOutput struct {
	Body struct {
		Total         int64            `json:"total"`
		TotalBytes    int64            `json:"total_bytes"`
		ByPurpose     map[string]int64 `json:"by_purpose"`
		ByContentType map[string]int64 `json:"by_content_type"`
		BySource      map[string]int64 `json:"by_source"`
		ByStatus      map[string]int64 `json:"by_status"`
	}
}

type docPresignInput struct {
	ID   string `path:"id" doc:"Asset ID" required:"true"`
	Body struct {
		ExpiresIn string `json:"expires_in" doc:"Signed URL TTL" example:"168h"`
	}
}

type docPresignOutput struct {
	Body struct {
		URL       string `json:"url"`
		ExpiresAt int64  `json:"expires_at"`
	}
}

type docPatchAssetInput struct {
	ID   string `path:"id" doc:"Asset ID" required:"true"`
	Body struct {
		Tags     []string       `json:"tags"`
		Metadata map[string]any `json:"metadata"`
	}
}

type docBulkDeleteInput struct {
	Body struct {
		IDs []string `json:"ids" maxItems:"100"`
	}
}

type docCreateExternalInput struct {
	Body struct {
		URL         string         `json:"url" doc:"External http or https URL"`
		Purpose     string         `json:"purpose" enum:"assistants,batch,fine-tune,media,vector-store,general"`
		Filename    string         `json:"filename"`
		Tags        []string       `json:"tags"`
		Source      string         `json:"source" example:"external-url"`
		Metadata    map[string]any `json:"metadata"`
		ContentType string         `json:"content_type"`
	}
}

type docCreateUploadInput struct {
	Body struct {
		Filename    string         `json:"filename" required:"true"`
		Purpose     string         `json:"purpose" required:"true" enum:"assistants,batch,fine-tune,media,vector-store,general"`
		ContentType string         `json:"content_type"`
		TotalBytes  int64          `json:"total_bytes"`
		Tags        []string       `json:"tags"`
		Metadata    map[string]any `json:"metadata"`
		Source      string         `json:"source"`
	}
}

type docCreateUploadOutput struct {
	Body struct {
		UploadID  string `json:"upload_id"`
		ChunkSize int64  `json:"chunk_size"`
		ExpiresAt int64  `json:"expires_at"`
	}
}

type docCompleteUploadInput struct {
	ID   string `path:"id" doc:"Upload session ID" required:"true"`
	Body struct {
		Parts []struct {
			Part int    `json:"part"`
			ETag string `json:"etag"`
		} `json:"parts"`
	}
}

type docOKOutput struct {
	Body map[string]any `json:"body"`
}

func registerOpenAPIDocs(api huma.API) {
	security := []map[string][]string{{"BearerAuth": {}}, {"APIKeyAuth": {}}}
	registerDoc[struct{}, struct{}](api, http.MethodGet, "/healthz", "healthz", "Health", "Health check", nil)
	registerDoc[struct{}, struct{}](api, http.MethodGet, "/metrics", "metrics", "Metrics", "Prometheus metrics", nil)

	registerDoc[struct{}, docAssetOutput](api, http.MethodPost, "/v1/files", "create-file", "Files", "Upload OpenAI-compatible file", security)
	registerDoc[docListFilesInput, docListOutput](api, http.MethodGet, "/v1/files", "list-files", "Files", "List OpenAI-compatible files", security)
	registerDoc[docIDInput, docAssetOutput](api, http.MethodGet, "/v1/files/{id}", "get-file", "Files", "Get OpenAI-compatible file metadata", security)
	registerDoc[docIDInput, docOKOutput](api, http.MethodDelete, "/v1/files/{id}", "delete-file", "Files", "Delete OpenAI-compatible file", security)
	registerDoc[docDownloadInput, struct{}](api, http.MethodGet, "/v1/files/{id}/content", "download-file", "Files", "Download OpenAI-compatible file content", security)

	registerDoc[docCreateExternalInput, docAssetOutput](api, http.MethodPost, "/v1/assets", "create-asset", "Assets", "Create asset by upload or external URL", security)
	registerDoc[docListAssetsInput, docListOutput](api, http.MethodGet, "/v1/assets", "list-assets", "Assets", "Search and list assets", security)
	registerDoc[struct{}, docStatsOutput](api, http.MethodGet, "/v1/assets/stats", "asset-stats", "Assets", "Get asset statistics", security)
	registerDoc[docBulkDeleteInput, docOKOutput](api, http.MethodPost, "/v1/assets/bulk-delete", "bulk-delete-assets", "Assets", "Bulk delete assets", security)
	registerDoc[docIDInput, docAssetOutput](api, http.MethodGet, "/v1/assets/{id}", "get-asset", "Assets", "Get asset metadata", security)
	registerDoc[docPatchAssetInput, docAssetOutput](api, http.MethodPatch, "/v1/assets/{id}", "patch-asset", "Assets", "Update asset tags and metadata", security)
	registerDoc[docIDInput, docOKOutput](api, http.MethodDelete, "/v1/assets/{id}", "delete-asset", "Assets", "Delete asset", security)
	registerDoc[docPresignInput, docPresignOutput](api, http.MethodPost, "/v1/assets/{id}/presign", "presign-asset", "Assets", "Create presigned download URL", security)
	registerDoc[docDownloadInput, struct{}](api, http.MethodGet, "/v1/assets/{id}/content", "download-asset", "Assets", "Download asset content", security)
	registerDoc[docThumbnailInput, struct{}](api, http.MethodGet, "/v1/assets/{id}/thumbnail", "asset-thumbnail", "Assets", "Get or generate asset thumbnail", security)

	registerDoc[docSignedDownloadInput, struct{}](api, http.MethodGet, "/v1/dl/{id}", "signed-download", "Downloads", "Download via signed URL", nil)
	registerDoc[docCreateUploadInput, docCreateUploadOutput](api, http.MethodPost, "/v1/uploads", "create-upload", "Uploads", "Create chunk upload session", security)
	registerDoc[docPartInput, docOKOutput](api, http.MethodPut, "/v1/uploads/{id}/parts/{part}", "upload-part", "Uploads", "Upload one chunk", security)
	registerDoc[docCompleteUploadInput, docAssetOutput](api, http.MethodPost, "/v1/uploads/{id}/complete", "complete-upload", "Uploads", "Complete chunk upload", security)
	registerDoc[docUploadIDInput, docOKOutput](api, http.MethodDelete, "/v1/uploads/{id}", "cancel-upload", "Uploads", "Cancel chunk upload", security)
}

func registerDoc[I, O any](api huma.API, method, path, operationID, tag, summary string, security []map[string][]string) {
	huma.Register(api, huma.Operation{
		OperationID: operationID,
		Method:      method,
		Path:        path,
		Tags:        []string{tag},
		Summary:     summary,
		Security:    security,
		Errors:      []int{400, 401, 403, 404, 409, 413, 429, 500},
	}, func(ctx context.Context, input *I) (*O, error) { return nil, nil })
}
