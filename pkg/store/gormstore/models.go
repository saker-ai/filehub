package gormstore

import (
	"time"

	"github.com/saker-ai/assethub/pkg/store"
)

type AssetModel struct {
	ID          string        `gorm:"type:text;primaryKey"`
	TenantID    string        `gorm:"type:text;not null;index:idx_asset_tenant;uniqueIndex:idx_asset_dedupe,where:dedupe_key IS NOT NULL"`
	Purpose     string        `gorm:"type:text;not null;index:idx_asset_purpose"`
	Filename    string        `gorm:"type:text;not null"`
	ContentType string        `gorm:"type:text"`
	Bytes       int64         `gorm:"not null;default:0"`
	StorageKey  string        `gorm:"type:text;not null;uniqueIndex"`
	Checksum    string        `gorm:"type:text;index:idx_asset_checksum"`
	DedupeKey   *string       `gorm:"type:text;uniqueIndex:idx_asset_dedupe,where:dedupe_key IS NOT NULL"`
	Status      string        `gorm:"type:text;not null;default:uploaded"`
	Source      string        `gorm:"type:text"`
	Metadata    store.JSONMap `gorm:"type:text"`
	CreatedAt   time.Time     `gorm:"not null;index:idx_asset_created"`
	UpdatedAt   time.Time     `gorm:"not null"`
	ExpiresAt   *time.Time    `gorm:"index:idx_asset_expires"`
	Tags        []TagModel    `gorm:"many2many:asset_tags;foreignKey:ID;joinForeignKey:AssetID;References:ID;joinReferences:TagID"`
}

func (AssetModel) TableName() string { return "assets" }

type TagModel struct {
	ID   int64  `gorm:"primaryKey;autoIncrement"`
	Name string `gorm:"type:text;uniqueIndex;not null"`
}

func (TagModel) TableName() string { return "tags" }

type AssetTagModel struct {
	AssetID string `gorm:"type:text;primaryKey;index:idx_asset_tag_asset"`
	TagID   int64  `gorm:"primaryKey;index:idx_asset_tag_tag"`
}

func (AssetTagModel) TableName() string { return "asset_tags" }

type AssetMetadataModel struct {
	AssetID   string `gorm:"type:text;primaryKey;index:idx_asset_metadata_asset"`
	Key       string `gorm:"type:text;primaryKey;index:idx_asset_metadata_key_value,priority:1"`
	ValueType string `gorm:"type:text;primaryKey;index:idx_asset_metadata_key_value,priority:2"`
	ValueText string `gorm:"type:text;primaryKey;index:idx_asset_metadata_key_value,priority:3"`
}

func (AssetMetadataModel) TableName() string { return "asset_metadata" }

type AIReviewModel struct {
	ID            string `gorm:"type:text;primaryKey"`
	TenantID      string `gorm:"type:text;not null;index:idx_ai_review_tenant_asset,priority:1"`
	AssetID       string `gorm:"type:text;not null;index:idx_ai_review_tenant_asset,priority:2;index:idx_ai_review_asset_created,priority:1"`
	Model         string `gorm:"type:text;index:idx_ai_review_model"`
	Verdict       string `gorm:"type:text;index:idx_ai_review_verdict"`
	Score         *float64
	Summary       string `gorm:"type:text"`
	Rubric        string `gorm:"type:text;index:idx_ai_review_rubric"`
	Confidence    *float64
	PromptVersion string        `gorm:"type:text"`
	ReviewJobID   string        `gorm:"type:text;index:idx_ai_review_job"`
	RawResponseID string        `gorm:"type:text"`
	Metadata      store.JSONMap `gorm:"type:text"`
	CreatedAt     time.Time     `gorm:"not null;index:idx_ai_review_asset_created,priority:2"`
	UpdatedAt     time.Time     `gorm:"not null"`
}

func (AIReviewModel) TableName() string { return "ai_reviews" }

type AssetReviewModel struct {
	ID              string                 `gorm:"type:text;primaryKey"`
	TenantID        string                 `gorm:"type:text;not null;index:idx_asset_review_tenant_status,priority:1"`
	Title           string                 `gorm:"type:text;not null"`
	Status          string                 `gorm:"type:text;not null;index:idx_asset_review_tenant_status,priority:2"`
	ReferenceID     string                 `gorm:"type:text;index:idx_asset_review_reference"`
	SelectedAssetID string                 `gorm:"type:text;index:idx_asset_review_selected"`
	Reviewer        string                 `gorm:"type:text;index:idx_asset_review_reviewer"`
	Source          string                 `gorm:"type:text;index:idx_asset_review_source"`
	TraceID         string                 `gorm:"type:text"`
	Metadata        store.JSONMap          `gorm:"type:text"`
	Items           []AssetReviewItemModel `gorm:"foreignKey:ReviewID;references:ID"`
	CreatedAt       time.Time              `gorm:"not null;index:idx_asset_review_created"`
	UpdatedAt       time.Time              `gorm:"not null"`
	CompletedAt     *time.Time             `gorm:"index:idx_asset_review_completed"`
}

func (AssetReviewModel) TableName() string { return "asset_reviews" }

type AssetReviewItemModel struct {
	ID        string `gorm:"type:text;primaryKey"`
	ReviewID  string `gorm:"type:text;not null;index:idx_asset_review_item_review;uniqueIndex:idx_asset_review_item_unique,priority:1"`
	AssetID   string `gorm:"type:text;not null;index:idx_asset_review_item_asset;uniqueIndex:idx_asset_review_item_unique,priority:2"`
	Decision  string `gorm:"type:text;index:idx_asset_review_item_decision"`
	Note      string `gorm:"type:text"`
	Score     *float64
	Metadata  store.JSONMap `gorm:"type:text"`
	CreatedAt time.Time     `gorm:"not null"`
	UpdatedAt time.Time     `gorm:"not null"`
}

func (AssetReviewItemModel) TableName() string { return "asset_review_items" }

type UploadSessionModel struct {
	ID               string          `gorm:"type:text;primaryKey"`
	TenantID         string          `gorm:"type:text;not null;index:idx_upload_tenant"`
	AssetID          string          `gorm:"type:text;index:idx_upload_asset"`
	Mode             string          `gorm:"type:text;not null;default:proxy"`
	Filename         string          `gorm:"type:text;not null"`
	Purpose          string          `gorm:"type:text;not null"`
	ContentType      string          `gorm:"type:text"`
	TotalBytes       int64           `gorm:"not null;default:0"`
	ChunkSize        int64           `gorm:"not null;default:10485760"`
	StorageKey       string          `gorm:"type:text"`
	ProviderUploadID string          `gorm:"type:text"`
	Status           string          `gorm:"type:text;not null;default:pending"`
	Source           string          `gorm:"type:text"`
	Metadata         store.JSONMap   `gorm:"type:text"`
	TagNames         store.JSONArray `gorm:"type:text"`
	CreatedAt        time.Time       `gorm:"not null"`
	ExpiresAt        time.Time       `gorm:"not null;index:idx_upload_expires"`
}

func (UploadSessionModel) TableName() string { return "upload_sessions" }

type UploadPartModel struct {
	UploadID string `gorm:"type:text;primaryKey;index:idx_part_upload"`
	PartNum  int    `gorm:"primaryKey"`
	ETag     string `gorm:"type:text;not null"`
	Bytes    int64  `gorm:"not null;default:0"`
}

func (UploadPartModel) TableName() string { return "upload_parts" }

func toAsset(m AssetModel) *store.Asset {
	tags := make([]store.Tag, 0, len(m.Tags))
	for _, t := range m.Tags {
		tags = append(tags, store.Tag{ID: t.ID, Name: t.Name})
	}
	return &store.Asset{
		ID:          m.ID,
		TenantID:    m.TenantID,
		Purpose:     m.Purpose,
		Filename:    m.Filename,
		ContentType: m.ContentType,
		Bytes:       m.Bytes,
		StorageKey:  m.StorageKey,
		Checksum:    m.Checksum,
		DedupeKey:   m.DedupeKey,
		Status:      m.Status,
		Source:      m.Source,
		Metadata:    m.Metadata,
		Tags:        tags,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
		ExpiresAt:   m.ExpiresAt,
	}
}

func fromAsset(a *store.Asset) AssetModel {
	return AssetModel{
		ID:          a.ID,
		TenantID:    a.TenantID,
		Purpose:     a.Purpose,
		Filename:    a.Filename,
		ContentType: a.ContentType,
		Bytes:       a.Bytes,
		StorageKey:  a.StorageKey,
		Checksum:    a.Checksum,
		DedupeKey:   a.DedupeKey,
		Status:      a.Status,
		Source:      a.Source,
		Metadata:    a.Metadata,
		CreatedAt:   a.CreatedAt,
		UpdatedAt:   a.UpdatedAt,
		ExpiresAt:   a.ExpiresAt,
	}
}

func toAIReview(m AIReviewModel) *store.AIReview {
	return &store.AIReview{
		ID:            m.ID,
		TenantID:      m.TenantID,
		AssetID:       m.AssetID,
		Model:         m.Model,
		Verdict:       m.Verdict,
		Score:         m.Score,
		Summary:       m.Summary,
		Rubric:        m.Rubric,
		Confidence:    m.Confidence,
		PromptVersion: m.PromptVersion,
		ReviewJobID:   m.ReviewJobID,
		RawResponseID: m.RawResponseID,
		Metadata:      m.Metadata,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}

func fromAIReview(r *store.AIReview) AIReviewModel {
	return AIReviewModel{
		ID:            r.ID,
		TenantID:      r.TenantID,
		AssetID:       r.AssetID,
		Model:         r.Model,
		Verdict:       r.Verdict,
		Score:         r.Score,
		Summary:       r.Summary,
		Rubric:        r.Rubric,
		Confidence:    r.Confidence,
		PromptVersion: r.PromptVersion,
		ReviewJobID:   r.ReviewJobID,
		RawResponseID: r.RawResponseID,
		Metadata:      r.Metadata,
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
	}
}

func toAssetReview(m AssetReviewModel) *store.AssetReview {
	items := make([]store.AssetReviewItem, 0, len(m.Items))
	for _, item := range m.Items {
		items = append(items, *toAssetReviewItem(item))
	}
	return &store.AssetReview{
		ID:              m.ID,
		TenantID:        m.TenantID,
		Title:           m.Title,
		Status:          m.Status,
		ReferenceID:     m.ReferenceID,
		SelectedAssetID: m.SelectedAssetID,
		Reviewer:        m.Reviewer,
		Source:          m.Source,
		TraceID:         m.TraceID,
		Metadata:        m.Metadata,
		Items:           items,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
		CompletedAt:     m.CompletedAt,
	}
}

func fromAssetReview(r *store.AssetReview) AssetReviewModel {
	items := make([]AssetReviewItemModel, 0, len(r.Items))
	for _, item := range r.Items {
		item.ReviewID = r.ID
		items = append(items, fromAssetReviewItem(&item))
	}
	return AssetReviewModel{
		ID:              r.ID,
		TenantID:        r.TenantID,
		Title:           r.Title,
		Status:          r.Status,
		ReferenceID:     r.ReferenceID,
		SelectedAssetID: r.SelectedAssetID,
		Reviewer:        r.Reviewer,
		Source:          r.Source,
		TraceID:         r.TraceID,
		Metadata:        r.Metadata,
		Items:           items,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
		CompletedAt:     r.CompletedAt,
	}
}

func toAssetReviewItem(m AssetReviewItemModel) *store.AssetReviewItem {
	return &store.AssetReviewItem{
		ID:        m.ID,
		ReviewID:  m.ReviewID,
		AssetID:   m.AssetID,
		Decision:  m.Decision,
		Note:      m.Note,
		Score:     m.Score,
		Metadata:  m.Metadata,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

func fromAssetReviewItem(item *store.AssetReviewItem) AssetReviewItemModel {
	return AssetReviewItemModel{
		ID:        item.ID,
		ReviewID:  item.ReviewID,
		AssetID:   item.AssetID,
		Decision:  item.Decision,
		Note:      item.Note,
		Score:     item.Score,
		Metadata:  item.Metadata,
		CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt,
	}
}

func toSession(m UploadSessionModel) *store.UploadSession {
	return &store.UploadSession{
		ID:               m.ID,
		TenantID:         m.TenantID,
		AssetID:          m.AssetID,
		Mode:             m.Mode,
		Filename:         m.Filename,
		Purpose:          m.Purpose,
		ContentType:      m.ContentType,
		TotalBytes:       m.TotalBytes,
		ChunkSize:        m.ChunkSize,
		StorageKey:       m.StorageKey,
		ProviderUploadID: m.ProviderUploadID,
		Status:           m.Status,
		Source:           m.Source,
		Metadata:         m.Metadata,
		TagNames:         []string(m.TagNames),
		CreatedAt:        m.CreatedAt,
		ExpiresAt:        m.ExpiresAt,
	}
}

func fromSession(s *store.UploadSession) UploadSessionModel {
	return UploadSessionModel{
		ID:               s.ID,
		TenantID:         s.TenantID,
		AssetID:          s.AssetID,
		Mode:             s.Mode,
		Filename:         s.Filename,
		Purpose:          s.Purpose,
		ContentType:      s.ContentType,
		TotalBytes:       s.TotalBytes,
		ChunkSize:        s.ChunkSize,
		StorageKey:       s.StorageKey,
		ProviderUploadID: s.ProviderUploadID,
		Status:           s.Status,
		Source:           s.Source,
		Metadata:         s.Metadata,
		TagNames:         store.JSONArray(s.TagNames),
		CreatedAt:        s.CreatedAt,
		ExpiresAt:        s.ExpiresAt,
	}
}
