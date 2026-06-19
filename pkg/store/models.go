package store

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrConflict      = errors.New("conflict")
	ErrForbidden     = errors.New("forbidden")
	ErrQuotaExceeded = errors.New("storage quota exceeded")
)

type JSONMap map[string]any

func (m JSONMap) Value() (driver.Value, error) {
	if m == nil {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	return string(b), err
}

func (m *JSONMap) Scan(value any) error {
	if value == nil {
		*m = JSONMap{}
		return nil
	}
	var data []byte
	switch v := value.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return errors.New("invalid JSONMap database value")
	}
	if len(data) == 0 {
		*m = JSONMap{}
		return nil
	}
	return json.Unmarshal(data, m)
}

type JSONArray []string

func (a JSONArray) Value() (driver.Value, error) {
	if a == nil {
		return "[]", nil
	}
	b, err := json.Marshal(a)
	return string(b), err
}

func (a *JSONArray) Scan(value any) error {
	if value == nil {
		*a = JSONArray{}
		return nil
	}
	var data []byte
	switch v := value.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return errors.New("invalid JSONArray database value")
	}
	if len(data) == 0 {
		*a = JSONArray{}
		return nil
	}
	return json.Unmarshal(data, a)
}

type Asset struct {
	ID          string
	TenantID    string
	Purpose     string
	Filename    string
	ContentType string
	Bytes       int64
	StorageKey  string
	Checksum    string
	DedupeKey   *string
	Status      string
	Source      string
	Metadata    JSONMap
	Tags        []Tag
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ExpiresAt   *time.Time
}

func CursorFromAsset(a *Asset) string {
	if a == nil {
		return ""
	}
	return fmt.Sprintf("%d:%s", a.CreatedAt.UnixNano(), a.ID)
}

type Tag struct {
	ID   int64
	Name string
}

type UploadSession struct {
	ID               string
	TenantID         string
	AssetID          string
	Mode             string
	Filename         string
	Purpose          string
	ContentType      string
	TotalBytes       int64
	ChunkSize        int64
	StorageKey       string
	ProviderUploadID string
	Status           string
	Source           string
	Metadata         JSONMap
	TagNames         []string
	CreatedAt        time.Time
	ExpiresAt        time.Time
}

type UploadPart struct {
	UploadID string
	PartNum  int
	ETag     string
	Bytes    int64
}

type AssetStats struct {
	Total         int64            `json:"total"`
	TotalBytes    int64            `json:"total_bytes"`
	ByPurpose     map[string]int64 `json:"by_purpose"`
	ByContentType map[string]int64 `json:"by_content_type"`
	BySource      map[string]int64 `json:"by_source"`
	ByStatus      map[string]int64 `json:"by_status"`
}

type AIReview struct {
	ID            string
	TenantID      string
	AssetID       string
	Model         string
	Verdict       string
	Score         *float64
	Summary       string
	Rubric        string
	Confidence    *float64
	PromptVersion string
	ReviewJobID   string
	RawResponseID string
	Metadata      JSONMap
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type AIReviewFilter struct {
	AssetID string
	Verdict string
	Model   string
	Limit   int
	Offset  int
}

type AssetReview struct {
	ID              string
	TenantID        string
	Title           string
	Status          string
	ReferenceID     string
	SelectedAssetID string
	Reviewer        string
	Source          string
	TraceID         string
	Metadata        JSONMap
	Items           []AssetReviewItem
	CreatedAt       time.Time
	UpdatedAt       time.Time
	CompletedAt     *time.Time
}

type AssetReviewItem struct {
	ID        string
	ReviewID  string
	AssetID   string
	Decision  string
	Note      string
	Score     *float64
	Metadata  JSONMap
	CreatedAt time.Time
	UpdatedAt time.Time
}

type AssetReviewFilter struct {
	Status   string
	Reviewer string
	Source   string
	Limit    int
	Offset   int
}

type AssetFilter struct {
	Purpose     string
	Status      string
	Tags        []string
	Filename    string
	Source      string
	ContentType string
	IDPrefix    string
	MetaModel   string
	MetaQuery   string
	Metadata    []MetadataFilter
	Limit       int
	Offset      int
	Order       string
	Cursor      string
	After       string
	Before      string
}

type MetadataFilter struct {
	Key      string
	Value    any
	HasValue bool
}

type AssetRepo interface {
	Create(ctx context.Context, a *Asset) error
	Get(ctx context.Context, tenantID, id string) (*Asset, error)
	FindByChecksum(ctx context.Context, tenantID, checksum string) (*Asset, error)
	List(ctx context.Context, tenantID string, filter AssetFilter) ([]*Asset, bool, error)
	Update(ctx context.Context, a *Asset) error
	Delete(ctx context.Context, tenantID, id string) error
	UpdateStatus(ctx context.Context, id, status string) error
	SetTags(ctx context.Context, id string, tags []string) error
	ListExpired(ctx context.Context, now time.Time, limit int) ([]*Asset, error)
	Stats(ctx context.Context, tenantID string) (*AssetStats, error)
}

type AIReviewRepo interface {
	CreateAIReview(ctx context.Context, r *AIReview) error
	GetAIReview(ctx context.Context, tenantID, id string) (*AIReview, error)
	ListAIReviews(ctx context.Context, tenantID string, filter AIReviewFilter) ([]*AIReview, error)
	DeleteAIReviewsForAsset(ctx context.Context, tenantID, assetID string) error
}

type AssetReviewRepo interface {
	CreateAssetReview(ctx context.Context, r *AssetReview) error
	GetAssetReview(ctx context.Context, tenantID, id string) (*AssetReview, error)
	ListAssetReviews(ctx context.Context, tenantID string, filter AssetReviewFilter) ([]*AssetReview, error)
	UpdateAssetReview(ctx context.Context, r *AssetReview) error
	UpdateAssetReviewItem(ctx context.Context, tenantID, reviewID string, item *AssetReviewItem) error
	DeleteAssetReviewsForAsset(ctx context.Context, tenantID, assetID string) error
}

type UploadRepo interface {
	CreateSession(ctx context.Context, s *UploadSession) error
	GetSession(ctx context.Context, tenantID, id string) (*UploadSession, error)
	AddPart(ctx context.Context, part *UploadPart) error
	ListParts(ctx context.Context, uploadID string) ([]*UploadPart, error)
	UpdateSessionStatus(ctx context.Context, id, status string) error
	DeleteSession(ctx context.Context, id string) error
	ListExpiredSessions(ctx context.Context, now time.Time, limit int) ([]*UploadSession, error)
}
