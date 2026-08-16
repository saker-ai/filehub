// Package workspace implements the FileHub Workspace domain: shared,
// tenant-scoped directories whose entries are versioned by immutable
// revisions that reference FileHub assets.
//
// The package contains the domain types, path validation rules, conflict
// path derivation and the transactional commit service. Persistence is
// provided by the gormrepo sub-package; HTTP transport by pkg/workspaceapi.
package workspace

import (
	"errors"
	"time"
)

// Operation and change kinds.
const (
	KindPut      = "put"
	KindDelete   = "delete"
	KindConflict = "conflict"
)

// Read event kinds.
const (
	ReadKindHuman = "human"
	ReadKindAgent = "agent"
	ReadKindShare = "share"
)

// Operation resolutions reported in commit results.
const (
	ResolutionApplied  = "applied"
	ResolutionConflict = "conflict"
)

// Sentinel errors returned by the service. Handlers map them to HTTP
// status codes (see pkg/workspaceapi).
var (
	// ErrNotFound is reused from the store layer semantics: the workspace,
	// entry, revision or share does not exist for this tenant.
	ErrNotFound = errors.New("workspace: not found")
	// ErrGone is returned by sync APIs when the workspace is soft-deleted.
	ErrGone = errors.New("workspace: deleted")
	// ErrConflictDigest is returned when an idempotency key is reused with a
	// different canonical request body.
	ErrConflictDigest = errors.New("workspace: idempotency key reused with different request")
	// ErrInvalidPath marks path validation failures.
	ErrInvalidPath = errors.New("workspace: invalid path")
	// ErrExcludedPath marks paths that must never be synchronized.
	ErrExcludedPath = errors.New("workspace: path is excluded from sync")
	// ErrInvalidOperation marks malformed commit operations.
	ErrInvalidOperation = errors.New("workspace: invalid operation")
	// ErrInvalidAsset marks referenced assets that cannot be referenced
	// (missing, foreign tenant, failed status, expired or incomplete).
	ErrInvalidAsset = errors.New("workspace: asset cannot be referenced")
	// ErrLimitExceeded marks requests exceeding configured hard limits.
	ErrLimitExceeded = errors.New("workspace: limit exceeded")
	// ErrPayloadTooLarge marks commit bodies over the size limit.
	ErrPayloadTooLarge = errors.New("workspace: payload too large")
	// ErrInvalidShareToken marks malformed share tokens.
	ErrInvalidShareToken = errors.New("workspace: invalid share token")
)

// Workspace is a tenant-scoped shared directory.
type Workspace struct {
	ID          string
	TenantID    string
	Name        string
	Description string
	// Sequence is the highest committed change sequence. It only grows,
	// and only inside commit transactions.
	Sequence  int64
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

// Entry is the current state of one path inside a workspace.
// Primary key is (WorkspaceID, Path). Directories are derived from file
// paths; empty directories are not synchronized.
type Entry struct {
	WorkspaceID string
	Path        string
	RevisionID  string
	Deleted     bool
	UpdatedAt   time.Time
}

// Revision is one immutable content version of a path. Delete revisions
// carry an empty AssetID. Revisions are never modified in place.
type Revision struct {
	ID             string
	WorkspaceID    string
	Path           string
	AssetID        string
	Kind           string // KindPut or KindDelete
	Bytes          int64
	Checksum       string
	Mode           uint32 // plain rwx permission bits only
	BaseRevisionID string
	ActorID        string
	DeviceID       string
	SessionID      string
	Note           string
	CreatedAt      time.Time
}

// Change is one ordered mutation inside a workspace. Primary key is
// (WorkspaceID, Sequence). Changes are the incremental sync cursor source;
// timestamps are never used as cursors.
type Change struct {
	WorkspaceID string
	Sequence    int64
	RevisionID  string
	Path        string
	Kind        string // KindPut, KindDelete or KindConflict
	CreatedAt   time.Time
}

// CommitReceipt records one atomic commit for idempotent retries.
// Unique key is (WorkspaceID, DeviceID, RequestID).
type CommitReceipt struct {
	WorkspaceID   string
	DeviceID      string
	RequestID     string
	RequestDigest string // canonical request SHA-256 hex
	FromSequence  int64
	ToSequence    int64
	Response      []byte // full response JSON of the first execution
	CreatedAt     time.Time
}

// ShareLink is a public, revocable share for one path. Only the SHA-256
// hash of the token is persisted; the plain token is returned exactly once
// at creation. TokenPrefix keeps the first token characters so list
// responses can show a hint without exposing the token.
type ShareLink struct {
	ID          string
	TokenHash   string
	TokenPrefix string
	WorkspaceID string
	Path        string
	CreatorID   string
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
	CreatedAt   time.Time
}

// ReadEvent is one UTC-day aggregate of read activity for a path.
// Unique key is (WorkspaceID, Path, Kind, Day). Actor fields keep the most
// recent contributor for debugging; stats endpoints never expose them.
type ReadEvent struct {
	WorkspaceID string
	Path        string
	Kind        string // ReadKindHuman, ReadKindAgent or ReadKindShare
	ActorID     string
	DeviceID    string
	SessionID   string
	Day         string // UTC day, format 2006-01-02
	Count       int64
	LastReadAt  time.Time
}

// ReadStat is one aggregated read counter row returned by stats queries.
type ReadStat struct {
	Path  string
	Day   string
	Count int64
}

// Operation is one requested mutation inside a commit.
type Operation struct {
	Kind           string
	Path           string
	AssetID        string
	BaseRevisionID string
	Mode           uint32
}

// CommitRequest is one atomic commit. RequestID carries the value of the
// Idempotency-Key request header.
type CommitRequest struct {
	DeviceID   string
	SessionID  string
	Note       string
	RequestID  string
	Operations []Operation
}

// RevisionInfo is the wire form of a revision inside commit results and
// change listings (doc §8.5).
type RevisionInfo struct {
	ID       string `json:"id"`
	Kind     string `json:"kind,omitempty"`
	AssetID  string `json:"asset_id,omitempty"`
	Bytes    int64  `json:"bytes,omitempty"`
	Checksum string `json:"checksum,omitempty"`
	Mode     uint32 `json:"mode,omitempty"`
}

// Info returns the wire form of the revision.
func (r *Revision) Info() *RevisionInfo {
	if r == nil {
		return nil
	}
	return &RevisionInfo{ID: r.ID, Kind: r.Kind, AssetID: r.AssetID, Bytes: r.Bytes, Checksum: r.Checksum, Mode: r.Mode}
}

// OperationResult describes how one operation was resolved.
type OperationResult struct {
	Index      int    `json:"index"`
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	Resolution string `json:"resolution"`
	Sequence   int64  `json:"sequence"`
	// FinalPath is the path that actually holds the submitted content:
	// the requested path when applied, the deterministic conflict path on
	// put conflicts (doc §8.5).
	FinalPath string `json:"final_path"`
	// Revision is the revision written by this operation. Nil for delete
	// conflicts where the remote version is kept.
	Revision *RevisionInfo `json:"revision,omitempty"`
	// KeptRevisionID is the remote revision kept for conflicts.
	KeptRevisionID string `json:"kept_revision_id,omitempty"`
}

// CommitResult is the outcome of one atomic commit. Its JSON form (minus
// the unexported-style fields tagged with "-") is what gets persisted in
// the CommitReceipt and replayed verbatim on retries.
type CommitResult struct {
	Object       string            `json:"object"`
	WorkspaceID  string            `json:"workspace_id"`
	DeviceID     string            `json:"device_id"`
	RequestID    string            `json:"request_id"`
	FromSequence int64             `json:"from_sequence"`
	ToSequence   int64             `json:"to_sequence"`
	Results      []OperationResult `json:"results"`

	// Response is the canonical JSON encoding of this result. Not part of
	// the encoded form itself.
	Response []byte `json:"-"`
	// Replayed reports that the result came from a stored receipt.
	Replayed bool `json:"-"`
}

// RestoreResult is the outcome of restoring a historical revision.
type RestoreResult struct {
	Object     string `json:"object"`
	Path       string `json:"path"`
	RevisionID string `json:"revision_id"`
	AssetID    string `json:"asset_id"`
	Bytes      int64  `json:"bytes"`
	Sequence   int64  `json:"sequence"`
}

// ShareResolution is what a public share token resolves to.
type ShareResolution struct {
	WorkspaceID string
	TenantID    string
	Path        string
	AssetID     string
}
