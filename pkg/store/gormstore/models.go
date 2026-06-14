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

type UploadSessionModel struct {
	ID               string          `gorm:"type:text;primaryKey"`
	TenantID         string          `gorm:"type:text;not null;index:idx_upload_tenant"`
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

func toSession(m UploadSessionModel) *store.UploadSession {
	return &store.UploadSession{
		ID:               m.ID,
		TenantID:         m.TenantID,
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
