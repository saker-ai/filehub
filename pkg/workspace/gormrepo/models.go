// Package gormrepo is the GORM persistence adapter for pkg/workspace.
//
// It shares the gormstore connection pool (via gormstore.Store.DB) and
// migrates its own tables through the same GORM AutoMigrate mechanism the
// asset tables use (doc §9, §13).
package gormrepo

import (
	"time"
)

// WorkspaceModel persists workspace.Workspace rows.
type WorkspaceModel struct {
	ID          string     `gorm:"type:text;primaryKey"`
	TenantID    string     `gorm:"type:text;not null;index:idx_ws_tenant_created,priority:1"`
	Name        string     `gorm:"type:text;not null"`
	Description string     `gorm:"type:text"`
	Sequence    int64      `gorm:"not null;default:0"`
	CreatedAt   time.Time  `gorm:"not null;index:idx_ws_tenant_created,priority:2,sort:desc"`
	UpdatedAt   time.Time  `gorm:"not null"`
	DeletedAt   *time.Time `gorm:"index:idx_ws_deleted"`
}

// TableName fixes the table name.
func (WorkspaceModel) TableName() string { return "workspaces" }

// EntryModel persists the current state of one path. Primary key is
// (WorkspaceID, Path) per doc §5.2.
type EntryModel struct {
	WorkspaceID string    `gorm:"type:text;primaryKey"`
	Path        string    `gorm:"type:text;primaryKey"`
	RevisionID  string    `gorm:"type:text;not null"`
	Deleted     bool      `gorm:"not null;default:false"`
	UpdatedAt   time.Time `gorm:"not null"`
}

// TableName fixes the table name.
func (EntryModel) TableName() string { return "workspace_entries" }

// RevisionModel persists immutable revisions. AssetID is indexed so the
// asset deletion paths can check workspace references (FH-11, doc §8.3).
type RevisionModel struct {
	ID             string    `gorm:"type:text;primaryKey"`
	WorkspaceID    string    `gorm:"type:text;not null;index:idx_wrev_ws_path_created,priority:1"`
	Path           string    `gorm:"type:text;not null;index:idx_wrev_ws_path_created,priority:2"`
	AssetID        string    `gorm:"type:text;index:idx_wrev_asset"`
	Kind           string    `gorm:"type:text;not null"`
	Bytes          int64     `gorm:"not null;default:0"`
	Checksum       string    `gorm:"type:text"`
	Mode           uint32    `gorm:"not null;default:0"`
	BaseRevisionID string    `gorm:"type:text"`
	ActorID        string    `gorm:"type:text"`
	DeviceID       string    `gorm:"type:text"`
	SessionID      string    `gorm:"type:text"`
	Note           string    `gorm:"type:text"`
	CreatedAt      time.Time `gorm:"not null;index:idx_wrev_ws_path_created,priority:3,sort:desc"`
}

// TableName fixes the table name.
func (RevisionModel) TableName() string { return "workspace_revisions" }

// ChangeModel persists the ordered change log. Primary key is
// (WorkspaceID, Sequence) per doc §5.4.
type ChangeModel struct {
	WorkspaceID string    `gorm:"type:text;primaryKey"`
	Sequence    int64     `gorm:"primaryKey;autoIncrement:false"`
	RevisionID  string    `gorm:"type:text;not null"`
	Path        string    `gorm:"type:text;not null"`
	Kind        string    `gorm:"type:text;not null"`
	CreatedAt   time.Time `gorm:"not null"`
}

// TableName fixes the table name.
func (ChangeModel) TableName() string { return "workspace_changes" }

// ReceiptModel persists commit idempotency receipts. The primary key is the
// documented unique key (WorkspaceID, DeviceID, RequestID) (doc §5.5).
type ReceiptModel struct {
	WorkspaceID   string    `gorm:"type:text;primaryKey"`
	DeviceID      string    `gorm:"type:text;primaryKey"`
	RequestID     string    `gorm:"type:text;primaryKey"`
	RequestDigest string    `gorm:"type:text;not null"`
	FromSequence  int64     `gorm:"not null"`
	ToSequence    int64     `gorm:"not null"`
	Response      string    `gorm:"type:text;not null"`
	CreatedAt     time.Time `gorm:"not null"`
}

// TableName fixes the table name.
func (ReceiptModel) TableName() string { return "workspace_commit_receipts" }

// ShareLinkModel persists public share links. Only the token hash is stored
// (SEC-04); TokenPrefix keeps a short hint for list responses.
type ShareLinkModel struct {
	ID          string     `gorm:"type:text;primaryKey"`
	TokenHash   string     `gorm:"type:text;not null;uniqueIndex"`
	TokenPrefix string     `gorm:"type:text;not null"`
	WorkspaceID string     `gorm:"type:text;not null;index:idx_share_ws_created,priority:1"`
	Path        string     `gorm:"type:text;not null"`
	CreatorID   string     `gorm:"type:text"`
	ExpiresAt   *time.Time `gorm:""`
	RevokedAt   *time.Time `gorm:""`
	CreatedAt   time.Time  `gorm:"not null;index:idx_share_ws_created,priority:2,sort:desc"`
}

// TableName fixes the table name.
func (ShareLinkModel) TableName() string { return "workspace_share_links" }

// ReadEventModel persists UTC-day aggregated read counters. The unique key
// is (WorkspaceID, Path, Kind, Day) per doc §5.6.
type ReadEventModel struct {
	WorkspaceID string    `gorm:"type:text;primaryKey"`
	Path        string    `gorm:"type:text;primaryKey"`
	Kind        string    `gorm:"type:text;primaryKey"`
	Day         string    `gorm:"type:text;primaryKey"`
	ActorID     string    `gorm:"type:text"`
	DeviceID    string    `gorm:"type:text"`
	SessionID   string    `gorm:"type:text"`
	Count       int64     `gorm:"not null;default:0"`
	LastReadAt  time.Time `gorm:"not null"`
}

// TableName fixes the table name.
func (ReadEventModel) TableName() string { return "workspace_read_events" }
