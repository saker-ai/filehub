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
		Object     string           `json:"object" example:"list"`
		Data       []map[string]any `json:"data"`
		HasMore    bool             `json:"has_more"`
		NextCursor string           `json:"next_cursor"`
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
		URL       string `json:"url" doc:"Provider-native public signed URL for S3/OSS; FileHub signed URL for local storage"`
		ExpiresAt int64  `json:"expires_at"`
	}
}

type docExternalAssetOutput struct {
	Body struct {
		ID          string `json:"id"`
		URL         string `json:"url" doc:"Provider-native public signed URL for S3/OSS; FileHub signed URL for local storage"`
		ContentType string `json:"contentType"`
		Size        int64  `json:"size"`
		ExpiresAt   int64  `json:"expiresAt"`
	}
}

type docExternalPresignInput struct {
	ID   string `path:"id" doc:"Asset ID" required:"true"`
	Body struct {
		ExpiresIn string `json:"expiresIn" doc:"Signed URL TTL" example:"168h"`
	}
}

type docExternalPresignOutput struct {
	Body struct {
		URL       string `json:"url" doc:"Provider-native public signed URL for S3/OSS; FileHub signed URL for local storage"`
		ExpiresAt int64  `json:"expiresAt"`
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

type docCreateAIReviewInput struct {
	ID   string `path:"id" doc:"Asset ID" required:"true"`
	Body struct {
		Model         string         `json:"model"`
		Verdict       string         `json:"verdict" enum:"approved,rejected,needs_revision,uncertain"`
		Score         *float64       `json:"score"`
		Summary       string         `json:"summary"`
		Rubric        string         `json:"rubric"`
		Confidence    *float64       `json:"confidence"`
		PromptVersion string         `json:"prompt_version"`
		ReviewJobID   string         `json:"review_job_id"`
		RawResponseID string         `json:"raw_response_id"`
		Metadata      map[string]any `json:"metadata"`
	}
}

type docListAIReviewsInput struct {
	AssetID string `query:"asset_id" doc:"Filter by asset ID"`
	Verdict string `query:"verdict" doc:"Filter by AI verdict"`
	Model   string `query:"model" doc:"Filter by review model"`
	Limit   int    `query:"limit" doc:"Maximum results" default:"20"`
	Offset  int    `query:"offset" doc:"Offset pagination" default:"0"`
}

type docCreateReviewInput struct {
	Body struct {
		Title           string         `json:"title"`
		Status          string         `json:"status" enum:"open,completed,archived"`
		ReferenceID     string         `json:"reference_asset_id"`
		AssetIDs        []string       `json:"asset_ids" required:"true"`
		SelectedAssetID string         `json:"selected_asset_id"`
		Reviewer        string         `json:"reviewer"`
		Source          string         `json:"source"`
		TraceID         string         `json:"trace_id"`
		Metadata        map[string]any `json:"metadata"`
	}
}

type docListReviewsInput struct {
	Status   string `query:"status" doc:"Filter by review status"`
	Reviewer string `query:"reviewer" doc:"Filter by reviewer"`
	Source   string `query:"source" doc:"Filter by review source"`
	Limit    int    `query:"limit" doc:"Maximum results" default:"20"`
	Offset   int    `query:"offset" doc:"Offset pagination" default:"0"`
}

type docPatchReviewInput struct {
	ID   string `path:"id" doc:"Review ID" required:"true"`
	Body struct {
		Title           string         `json:"title"`
		Status          string         `json:"status" enum:"open,completed,archived"`
		ReferenceID     string         `json:"reference_asset_id"`
		SelectedAssetID string         `json:"selected_asset_id"`
		Reviewer        string         `json:"reviewer"`
		Source          string         `json:"source"`
		TraceID         string         `json:"trace_id"`
		Metadata        map[string]any `json:"metadata"`
	}
}

type docPatchReviewItemInput struct {
	ID      string `path:"id" doc:"Review ID" required:"true"`
	AssetID string `path:"asset_id" doc:"Asset ID" required:"true"`
	Body    struct {
		Decision string         `json:"decision" enum:"pending,approved,rejected,needs_revision,best"`
		Note     string         `json:"note"`
		Score    *float64       `json:"score"`
		Metadata map[string]any `json:"metadata"`
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
		Mode        string         `json:"mode" enum:"proxy,direct,direct_multipart" doc:"Upload mode. Empty defaults to proxy."`
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
		UploadID     string            `json:"upload_id"`
		AssetID      string            `json:"asset_id"`
		Mode         string            `json:"mode"`
		ChunkSize    int64             `json:"chunk_size"`
		Method       string            `json:"method"`
		URL          string            `json:"url"`
		Headers      map[string]string `json:"headers"`
		URLExpiresAt int64             `json:"url_expires_at"`
		ExpiresAt    int64             `json:"expires_at"`
	}
}

type docCreateExternalUploadInput struct {
	Body struct {
		Mode        string         `json:"mode" enum:"direct,direct_multipart" doc:"Explicit provider upload mode."`
		Filename    string         `json:"filename" required:"true"`
		Purpose     string         `json:"purpose" required:"true" enum:"assistants,batch,fine-tune,media,vector-store,general"`
		ContentType string         `json:"contentType"`
		TotalBytes  int64          `json:"totalBytes"`
		Tags        []string       `json:"tags"`
		Metadata    map[string]any `json:"metadata"`
		Source      string         `json:"source"`
	}
}

type docCreateExternalUploadOutput struct {
	Body struct {
		UploadID     string            `json:"uploadId"`
		AssetID      string            `json:"assetId"`
		Mode         string            `json:"mode"`
		ChunkSize    int64             `json:"chunkSize"`
		Method       string            `json:"method"`
		URL          string            `json:"url"`
		Headers      map[string]string `json:"headers"`
		URLExpiresAt int64             `json:"urlExpiresAt"`
		ExpiresAt    int64             `json:"expiresAt"`
	}
}

type docPresignUploadOutput struct {
	Body struct {
		UploadID  string            `json:"upload_id"`
		AssetID   string            `json:"asset_id"`
		Method    string            `json:"method"`
		URL       string            `json:"url"`
		Headers   map[string]string `json:"headers"`
		ExpiresAt int64             `json:"expires_at"`
	}
}

type docPresignUploadPartOutput struct {
	Body struct {
		UploadID  string            `json:"upload_id"`
		AssetID   string            `json:"asset_id"`
		Part      int               `json:"part"`
		Method    string            `json:"method"`
		URL       string            `json:"url"`
		Headers   map[string]string `json:"headers"`
		ExpiresAt int64             `json:"expires_at"`
	}
}

type docExternalCapabilitiesOutput struct {
	Body struct {
		DirectUpload          bool     `json:"directUpload"`
		DirectMultipartUpload bool     `json:"directMultipartUpload"`
		MaxUploadBytes        int64    `json:"maxUploadBytes"`
		DefaultPartSize       int64    `json:"defaultPartSize"`
		MinPartSize           int64    `json:"minPartSize"`
		MaxPartSize           int64    `json:"maxPartSize"`
		ChecksumAlgorithms    []string `json:"checksumAlgorithms"`
	}
}

type docPresignExternalUploadPartOutput struct {
	Body struct {
		UploadID  string            `json:"uploadId"`
		AssetID   string            `json:"assetId"`
		Part      int               `json:"part"`
		Method    string            `json:"method"`
		URL       string            `json:"url"`
		Headers   map[string]string `json:"headers"`
		ExpiresAt int64             `json:"expiresAt"`
	}
}

type docCompleteUploadInput struct {
	ID   string `path:"id" doc:"Upload session ID" required:"true"`
	Body struct {
		Parts []struct {
			Part int    `json:"part"`
			ETag string `json:"etag"`
		} `json:"parts"`
		Checksum string `json:"checksum" doc:"Optional sha256:<hex> checksum verified before asset registration."`
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
	registerDoc[docCreateReviewInput, docOKOutput](api, http.MethodPost, "/v1/reviews", "create-review", "Reviews", "Create human asset review task", security)
	registerDoc[docListReviewsInput, docListOutput](api, http.MethodGet, "/v1/reviews", "list-reviews", "Reviews", "List human asset review tasks", security)
	registerDoc[docIDInput, docOKOutput](api, http.MethodGet, "/v1/reviews/{id}", "get-review", "Reviews", "Get human asset review task", security)
	registerDoc[docPatchReviewInput, docOKOutput](api, http.MethodPatch, "/v1/reviews/{id}", "patch-review", "Reviews", "Update human asset review task", security)
	registerDoc[docPatchReviewItemInput, docOKOutput](api, http.MethodPatch, "/v1/reviews/{id}/items/{asset_id}", "patch-review-item", "Reviews", "Update human review decision for one asset", security)
	registerDoc[docListAIReviewsInput, docListOutput](api, http.MethodGet, "/v1/ai-reviews", "list-ai-reviews", "AI Reviews", "List AI review results", security)
	registerDoc[docIDInput, docAssetOutput](api, http.MethodGet, "/v1/assets/{id}", "get-asset", "Assets", "Get asset metadata", security)
	registerDoc[docPatchAssetInput, docAssetOutput](api, http.MethodPatch, "/v1/assets/{id}", "patch-asset", "Assets", "Update asset tags and metadata", security)
	registerDoc[docIDInput, docOKOutput](api, http.MethodDelete, "/v1/assets/{id}", "delete-asset", "Assets", "Delete asset", security)
	registerDoc[docIDInput, docListOutput](api, http.MethodGet, "/v1/assets/{id}/ai-reviews", "list-asset-ai-reviews", "AI Reviews", "List AI review results for one asset", security)
	registerDoc[docCreateAIReviewInput, docOKOutput](api, http.MethodPost, "/v1/assets/{id}/ai-reviews", "create-asset-ai-review", "AI Reviews", "Record AI review result for one asset", security)
	registerDoc[docPresignInput, docPresignOutput](api, http.MethodPost, "/v1/assets/{id}/presign", "presign-asset", "Assets", "Create presigned download URL", security)
	registerDoc[docDownloadInput, struct{}](api, http.MethodGet, "/v1/assets/{id}/content", "download-asset", "Assets", "Download asset content", security)
	registerDoc[docThumbnailInput, struct{}](api, http.MethodGet, "/v1/assets/{id}/thumbnail", "asset-thumbnail", "Assets", "Get or generate asset thumbnail", security)
	registerDoc[docIDInput, struct{}](api, http.MethodGet, "/v1/assets/{id}/preview", "asset-preview", "Assets", "Browser-renderable preview; office documents are converted to PDF via LibreOffice when installed, else 404", security)

	registerDoc[struct{}, docExternalAssetOutput](api, http.MethodPost, "/v1/external/assets", "external-upload-asset", "External Asset API", "Upload an asset using multipart form data", security)
	registerDoc[struct{}, docExternalAssetOutput](api, http.MethodPut, "/v1/external/assets", "external-put-asset", "External Asset API", "Upload an asset as a byte stream", security)
	registerDoc[docIDInput, struct{}](api, http.MethodGet, "/v1/external/assets/{id}", "external-download-asset", "External Asset API", "Download asset content", security)
	registerDoc[docIDInput, struct{}](api, http.MethodHead, "/v1/external/assets/{id}", "external-head-asset", "External Asset API", "Check whether an asset exists", security)
	registerDoc[docExternalPresignInput, docExternalPresignOutput](api, http.MethodPost, "/v1/external/assets/{id}/presign", "external-presign-asset", "External Asset API", "Create a signed download URL", security)
	registerDoc[struct{}, docExternalCapabilitiesOutput](api, http.MethodGet, "/v1/external/capabilities", "external-capabilities", "External Asset API", "Discover direct upload capabilities and limits", security)
	registerDoc[docCreateExternalUploadInput, docCreateExternalUploadOutput](api, http.MethodPost, "/v1/external/uploads", "external-create-upload", "External Asset API", "Create a direct provider upload session", security)
	registerDoc[docPartInput, docPresignExternalUploadPartOutput](api, http.MethodPost, "/v1/external/uploads/{id}/parts/{part}/presign", "external-presign-upload-part", "External Asset API", "Create a direct multipart part upload URL", security)
	registerDoc[docCompleteUploadInput, docExternalAssetOutput](api, http.MethodPost, "/v1/external/uploads/{id}/complete", "external-complete-upload", "External Asset API", "Complete a direct provider upload", security)
	registerDoc[docUploadIDInput, docOKOutput](api, http.MethodDelete, "/v1/external/uploads/{id}", "external-cancel-upload", "External Asset API", "Cancel a direct provider upload", security)

	registerDoc[docSignedDownloadInput, struct{}](api, http.MethodGet, "/v1/dl/{id}", "signed-download", "Downloads", "Download via signed URL", nil)
	registerDoc[docCreateUploadInput, docCreateUploadOutput](api, http.MethodPost, "/v1/uploads", "create-upload", "Uploads", "Create upload session", security)
	registerDoc[docUploadIDInput, docPresignUploadOutput](api, http.MethodPost, "/v1/uploads/{id}/presign", "presign-upload", "Uploads", "Create direct upload URL", security)
	registerDoc[docPartInput, docPresignUploadPartOutput](api, http.MethodPost, "/v1/uploads/{id}/parts/{part}/presign", "presign-upload-part", "Uploads", "Create direct multipart part upload URL", security)
	registerDoc[docPartInput, docOKOutput](api, http.MethodPut, "/v1/uploads/{id}/parts/{part}", "upload-part", "Uploads", "Upload one chunk", security)
	registerDoc[docCompleteUploadInput, docAssetOutput](api, http.MethodPost, "/v1/uploads/{id}/complete", "complete-upload", "Uploads", "Complete upload", security)
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
