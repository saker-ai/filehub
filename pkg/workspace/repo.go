package workspace

import (
	"context"
	"time"

	"github.com/saker-ai/filehub/pkg/store"
)

// Repository is the persistence contract consumed by the Service.
// Implementations live in pkg/workspace/gormrepo. All methods take the
// context first and scope every query to the keys they receive.
//
// WithTx runs fn inside a single database transaction; the context passed
// to fn propagates the transaction to every other Repository method, so
// the Service can compose one atomic commit from these primitives.
type Repository interface {
	TxRunner
	WorkspaceRepo
	EntryRepo
	RevisionRepo
	ChangeRepo
	ReceiptRepo
	ShareRepo
	ReadEventRepo
}

// TxRunner executes work inside one database transaction.
type TxRunner interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// WorkspaceRepo covers workspace lifecycle rows.
type WorkspaceRepo interface {
	CreateWorkspace(ctx context.Context, ws *Workspace) error
	GetWorkspace(ctx context.Context, tenantID, id string) (*Workspace, error)
	// LockWorkspace loads the workspace row holding the commit lock
	// (SELECT ... FOR UPDATE on Postgres; SQLite serializes writers).
	LockWorkspace(ctx context.Context, tenantID, id string) (*Workspace, error)
	ListWorkspaces(ctx context.Context, tenantID, cursor string, limit int) ([]*Workspace, bool, error)
	UpdateWorkspace(ctx context.Context, ws *Workspace) error
	UpdateWorkspaceSequence(ctx context.Context, workspaceID string, sequence int64) error
	SoftDeleteWorkspace(ctx context.Context, tenantID, id string) error
}

// EntryRepo covers the per-path current state.
type EntryRepo interface {
	GetEntry(ctx context.Context, workspaceID, path string) (*Entry, error)
	UpsertEntry(ctx context.Context, entry *Entry) error
	ListEntries(ctx context.Context, workspaceID, prefix, cursor string, limit int) ([]*Entry, bool, error)
}

// RevisionRepo covers immutable revisions and asset references.
type RevisionRepo interface {
	CreateRevision(ctx context.Context, rev *Revision) error
	GetRevision(ctx context.Context, workspaceID, id string) (*Revision, error)
	GetRevisions(ctx context.Context, workspaceID string, ids []string) ([]*Revision, error)
	ListRevisions(ctx context.Context, workspaceID, path, cursor string, limit int) ([]*Revision, bool, error)
	// AssetReferenced reports whether any revision references the asset.
	AssetReferenced(ctx context.Context, assetID string) (bool, error)
	// ClearAssetExpiry removes expiry eligibility from referenced assets
	// (UPDATE assets SET expires_at = NULL WHERE id IN ...).
	ClearAssetExpiry(ctx context.Context, assetIDs []string) error
}

// ChangeRepo covers the ordered change log.
type ChangeRepo interface {
	CreateChange(ctx context.Context, ch *Change) error
	ListChanges(ctx context.Context, workspaceID string, after int64, limit int) ([]*Change, bool, error)
}

// ReceiptRepo covers commit idempotency receipts.
type ReceiptRepo interface {
	GetReceipt(ctx context.Context, workspaceID, deviceID, requestID string) (*CommitReceipt, error)
	CreateReceipt(ctx context.Context, receipt *CommitReceipt) error
}

// ShareRepo covers public share links.
type ShareRepo interface {
	CreateShare(ctx context.Context, share *ShareLink) error
	GetShare(ctx context.Context, workspaceID, shareID string) (*ShareLink, error)
	GetShareByTokenHash(ctx context.Context, tokenHash string) (*ShareLink, *Workspace, error)
	ListShares(ctx context.Context, workspaceID string, offset, limit int) ([]*ShareLink, bool, error)
	// RevokeShare marks an unrevoked share revoked by share ID; it
	// returns false when no matching unrevoked share exists.
	RevokeShare(ctx context.Context, workspaceID, shareID string, at time.Time) (bool, error)
}

// ReadEventRepo covers aggregated read statistics.
type ReadEventRepo interface {
	RecordReadEvents(ctx context.Context, events []*ReadEvent) error
	ReadTotalsByPath(ctx context.Context, workspaceID string, paths []string) (map[string]int64, error)
	ReadStats(ctx context.Context, workspaceID, prefix string, sinceDay string) ([]ReadStat, error)
}

// AssetLookup resolves assets referenced by commit operations. It is
// satisfied by the existing store.AssetRepo (gormstore) whose Get already
// scopes to the tenant and returns store.ErrNotFound otherwise.
type AssetLookup interface {
	Get(ctx context.Context, tenantID, id string) (*store.Asset, error)
}
