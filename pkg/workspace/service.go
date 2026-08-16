package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/saker-ai/filehub/pkg/store"
)

// AssetReferenceChecker guards the existing asset deletion paths (single
// delete, bulk delete, GC) against workspace revision references (FH-11,
// doc §8.3). A nil checker keeps the historical behavior.
type AssetReferenceChecker interface {
	AssetReferenced(ctx context.Context, tenantID, assetID string) (bool, error)
}

// Service implements the workspace domain logic on top of the consumer
// defined Repository and AssetLookup interfaces.
type Service struct {
	repo   Repository
	assets AssetLookup
	limits Limits
}

// New constructs a Service. limits are clamped so configuration can lower
// but never raise the documented hard limits.
func New(repo Repository, assets AssetLookup, limits Limits) *Service {
	return &Service{repo: repo, assets: assets, limits: ClampLimits(limits)}
}

// Limits returns the effective hard limits.
func (s *Service) Limits() Limits { return s.limits }

// asset statuses that a commit may reference (doc §8.3, R-03/R-11).
var referenceableStatuses = map[string]bool{
	"uploaded":   true,
	"processing": true,
	"ready":      true,
}

const (
	idWorkspacePrefix = "ws-"
	idRevisionPrefix  = "wrev-"
	idSharePrefix     = "share-"

	maxWorkspaceNameBytes               = 256
	maxWorkspaceDescriptionBytes        = 1024
	maxIdentityBytes                    = 256 // device_id / session_id / request_id
	maxModeBits                         = 0o777
	defaultFileMode              uint32 = 0o644
)

func newID(prefix string) string {
	return prefix + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
}

// --- Workspace lifecycle -------------------------------------------------

// CreateWorkspace creates a new workspace. Names may repeat inside a
// tenant; IDs are globally unique.
func (s *Service) CreateWorkspace(ctx context.Context, tenantID, name, description string) (*Workspace, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("%w: name is required", ErrInvalidOperation)
	}
	if len(name) > maxWorkspaceNameBytes {
		return nil, fmt.Errorf("%w: name exceeds %d bytes", ErrLimitExceeded, maxWorkspaceNameBytes)
	}
	if len(description) > maxWorkspaceDescriptionBytes {
		return nil, fmt.Errorf("%w: description exceeds %d bytes", ErrLimitExceeded, maxWorkspaceDescriptionBytes)
	}
	now := time.Now().UTC()
	ws := &Workspace{
		ID:          newID(idWorkspacePrefix),
		TenantID:    tenantID,
		Name:        name,
		Description: description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.repo.CreateWorkspace(ctx, ws); err != nil {
		return nil, err
	}
	return ws, nil
}

// ListWorkspaces lists tenant workspaces, newest first, with an opaque
// cursor pointing at the last item of the previous page.
func (s *Service) ListWorkspaces(ctx context.Context, tenantID, cursor string, limit int) ([]*Workspace, bool, error) {
	return s.repo.ListWorkspaces(ctx, tenantID, cursor, ClampListLimit(limit))
}

// GetWorkspace returns the workspace even when soft-deleted so management
// endpoints can show its state.
func (s *Service) GetWorkspace(ctx context.Context, tenantID, id string) (*Workspace, error) {
	ws, err := s.repo.GetWorkspace(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	return ws, nil
}

// PatchWorkspace updates name and/or description. Sync-style 410 applies
// to soft-deleted workspaces.
func (s *Service) PatchWorkspace(ctx context.Context, tenantID, id string, name, description *string) (*Workspace, error) {
	ws, err := s.activeWorkspace(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if name != nil {
		trimmed := strings.TrimSpace(*name)
		if trimmed == "" {
			return nil, fmt.Errorf("%w: name must not be empty", ErrInvalidOperation)
		}
		if len(trimmed) > maxWorkspaceNameBytes {
			return nil, fmt.Errorf("%w: name exceeds %d bytes", ErrLimitExceeded, maxWorkspaceNameBytes)
		}
		ws.Name = trimmed
	}
	if description != nil {
		if len(*description) > maxWorkspaceDescriptionBytes {
			return nil, fmt.Errorf("%w: description exceeds %d bytes", ErrLimitExceeded, maxWorkspaceDescriptionBytes)
		}
		ws.Description = *description
	}
	if err := s.repo.UpdateWorkspace(ctx, ws); err != nil {
		return nil, err
	}
	return ws, nil
}

// DeleteWorkspace soft-deletes; assets are never deleted and clients are
// never asked to remove local files (doc §8.1).
func (s *Service) DeleteWorkspace(ctx context.Context, tenantID, id string) error {
	ws, err := s.repo.GetWorkspace(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if ws.DeletedAt != nil {
		return nil
	}
	return s.repo.SoftDeleteWorkspace(ctx, tenantID, id)
}

// activeWorkspace loads a workspace for sync endpoints: missing or foreign
// tenant yields ErrNotFound, soft-deleted yields ErrGone (410).
func (s *Service) activeWorkspace(ctx context.Context, tenantID, id string) (*Workspace, error) {
	ws, err := s.repo.GetWorkspace(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if ws.DeletedAt != nil {
		return nil, ErrGone
	}
	return ws, nil
}

// --- Browsing ------------------------------------------------------------

// TreeNode is one tree row: the entry joined with its current revision.
type TreeNode struct {
	Path       string
	RevisionID string
	Kind       string
	AssetID    string
	Bytes      int64
	Checksum   string
	Mode       uint32
	UpdatedAt  time.Time
}

// Tree lists live entries sorted by path ascending (doc §8.2/§8.5).
// cursor is the last path of the previous page.
func (s *Service) Tree(ctx context.Context, tenantID, workspaceID, prefix, cursor string, limit int) ([]TreeNode, bool, error) {
	if _, err := s.activeWorkspace(ctx, tenantID, workspaceID); err != nil {
		return nil, false, err
	}
	entries, hasMore, err := s.repo.ListEntries(ctx, workspaceID, prefix, cursor, ClampListLimit(limit))
	if err != nil {
		return nil, false, err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.RevisionID)
	}
	revisions, err := s.revisionMap(ctx, workspaceID, ids)
	if err != nil {
		return nil, false, err
	}
	nodes := make([]TreeNode, 0, len(entries))
	for _, e := range entries {
		node := TreeNode{Path: e.Path, RevisionID: e.RevisionID, UpdatedAt: e.UpdatedAt}
		if rev := revisions[e.RevisionID]; rev != nil {
			node.Kind = rev.Kind
			node.AssetID = rev.AssetID
			node.Bytes = rev.Bytes
			node.Checksum = rev.Checksum
			node.Mode = rev.Mode
		}
		nodes = append(nodes, node)
	}
	return nodes, hasMore, nil
}

// EntryView is the current entry plus its revision (doc §8.5).
type EntryView struct {
	Path     string
	Revision *Revision
}

// GetEntry returns the current entry for one path; missing or deleted
// paths yield ErrNotFound.
func (s *Service) GetEntry(ctx context.Context, tenantID, workspaceID, p string) (*EntryView, error) {
	if _, err := s.activeWorkspace(ctx, tenantID, workspaceID); err != nil {
		return nil, err
	}
	if err := ValidatePath(p, s.limits); err != nil {
		return nil, err
	}
	entry, err := s.repo.GetEntry(ctx, workspaceID, p)
	if err != nil {
		return nil, err
	}
	if entry.Deleted {
		return nil, ErrNotFound
	}
	rev, err := s.repo.GetRevision(ctx, workspaceID, entry.RevisionID)
	if err != nil {
		return nil, err
	}
	return &EntryView{Path: entry.Path, Revision: rev}, nil
}

// History lists revisions for one path (or the whole workspace when path
// is empty), newest first. cursor is the opaque key of the last item.
func (s *Service) History(ctx context.Context, tenantID, workspaceID, p, cursor string, limit int) ([]*Revision, bool, error) {
	if _, err := s.activeWorkspace(ctx, tenantID, workspaceID); err != nil {
		return nil, false, err
	}
	if p != "" {
		if err := ValidatePath(p, s.limits); err != nil {
			return nil, false, err
		}
	}
	return s.repo.ListRevisions(ctx, workspaceID, p, cursor, ClampListLimit(limit))
}

// ChangeView is one change log row joined with its revision.
type ChangeView struct {
	Sequence int64
	Path     string
	Kind     string
	Revision *Revision
}

// ListChanges returns changes with sequence strictly greater than after,
// ascending (doc §8.2/§8.5).
func (s *Service) ListChanges(ctx context.Context, tenantID, workspaceID string, after int64, limit int) ([]ChangeView, int64, bool, error) {
	if _, err := s.activeWorkspace(ctx, tenantID, workspaceID); err != nil {
		return nil, after, false, err
	}
	changes, hasMore, err := s.repo.ListChanges(ctx, workspaceID, after, ClampListLimit(limit))
	if err != nil {
		return nil, after, false, err
	}
	ids := make([]string, 0, len(changes))
	for _, ch := range changes {
		if ch.RevisionID != "" {
			ids = append(ids, ch.RevisionID)
		}
	}
	revisions, err := s.revisionMap(ctx, workspaceID, ids)
	if err != nil {
		return nil, after, false, err
	}
	views := make([]ChangeView, 0, len(changes))
	next := after
	for _, ch := range changes {
		views = append(views, ChangeView{Sequence: ch.Sequence, Path: ch.Path, Kind: ch.Kind, Revision: revisions[ch.RevisionID]})
		next = ch.Sequence
	}
	return views, next, hasMore, nil
}

func (s *Service) revisionMap(ctx context.Context, workspaceID string, ids []string) (map[string]*Revision, error) {
	out := map[string]*Revision{}
	if len(ids) == 0 {
		return out, nil
	}
	revisions, err := s.repo.GetRevisions(ctx, workspaceID, ids)
	if err != nil {
		return nil, err
	}
	for _, rev := range revisions {
		out[rev.ID] = rev
	}
	return out, nil
}

// --- Commit --------------------------------------------------------------

// canonicalCommitRequest is the fixed struct whose encoding/json output
// defines the commit request digest (doc §8.3, R-02). Field order and
// operation array order are exactly as submitted; nothing is excluded.
type canonicalCommitRequest struct {
	DeviceID   string               `json:"device_id"`
	SessionID  string               `json:"session_id"`
	Note       string               `json:"note"`
	RequestID  string               `json:"request_id"`
	Operations []canonicalOperation `json:"operations"`
}

type canonicalOperation struct {
	Kind           string `json:"kind"`
	Path           string `json:"path"`
	AssetID        string `json:"asset_id"`
	BaseRevisionID string `json:"base_revision_id"`
	Mode           uint32 `json:"mode"`
}

// RequestDigest returns the canonical SHA-256 hex digest of a commit
// request. Same logical request, same digest — independent of JSON field
// order or whitespace in the transport body.
func RequestDigest(req CommitRequest) string {
	canon := canonicalCommitRequest{
		DeviceID:   req.DeviceID,
		SessionID:  req.SessionID,
		Note:       req.Note,
		RequestID:  req.RequestID,
		Operations: make([]canonicalOperation, len(req.Operations)),
	}
	for i, op := range req.Operations {
		canon.Operations[i] = canonicalOperation{
			Kind:           op.Kind,
			Path:           op.Path,
			AssetID:        op.AssetID,
			BaseRevisionID: op.BaseRevisionID,
			Mode:           op.Mode,
		}
	}
	data, err := json.Marshal(canon)
	if err != nil {
		// Fixed concrete struct with plain string/integer fields:
		// encoding/json cannot fail.
		panic(fmt.Sprintf("workspace: canonical commit marshal: %v", err))
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Commit applies one atomic commit per doc §8.3: all operations succeed
// together inside a single transaction or the whole request rolls back.
// Retries with the same (workspace, device, request) key and identical
// canonical body replay the stored receipt; a different body yields
// ErrConflictDigest.
func (s *Service) Commit(ctx context.Context, tenantID, workspaceID string, req CommitRequest, actorID string) (*CommitResult, error) {
	if err := s.validateCommitRequest(req); err != nil {
		return nil, err
	}
	if _, err := s.activeWorkspace(ctx, tenantID, workspaceID); err != nil {
		return nil, err
	}
	assetsByID, err := s.validateReferencedAssets(ctx, tenantID, req.Operations)
	if err != nil {
		return nil, err
	}
	digest := RequestDigest(req)
	if replayed, err := s.replayReceipt(ctx, workspaceID, req, digest); err != nil || replayed != nil {
		return replayed, err
	}

	assetIDs := referencedAssetIDs(req.Operations)
	var result *CommitResult
	txErr := s.repo.WithTx(ctx, func(ctx context.Context) error {
		ws, err := s.repo.LockWorkspace(ctx, tenantID, workspaceID)
		if err != nil {
			return err
		}
		if ws.DeletedAt != nil {
			return ErrGone
		}
		applied, err := s.applyOperations(ctx, ws, req, actorID, assetsByID)
		if err != nil {
			return err
		}
		toSequence := ws.Sequence + int64(len(req.Operations))
		if err := s.repo.UpdateWorkspaceSequence(ctx, workspaceID, toSequence); err != nil {
			return err
		}
		if len(assetIDs) > 0 {
			if err := s.repo.ClearAssetExpiry(ctx, assetIDs); err != nil {
				return err
			}
		}
		res := &CommitResult{
			Object:       "commit_receipt",
			WorkspaceID:  workspaceID,
			DeviceID:     req.DeviceID,
			RequestID:    req.RequestID,
			FromSequence: ws.Sequence + 1,
			ToSequence:   toSequence,
			Results:      applied,
		}
		body, err := json.Marshal(res)
		if err != nil {
			return err
		}
		receipt := &CommitReceipt{
			WorkspaceID:   workspaceID,
			DeviceID:      req.DeviceID,
			RequestID:     req.RequestID,
			RequestDigest: digest,
			FromSequence:  res.FromSequence,
			ToSequence:    res.ToSequence,
			Response:      body,
			CreatedAt:     time.Now().UTC(),
		}
		if err := s.repo.CreateReceipt(ctx, receipt); err != nil {
			return err
		}
		res.Response = body
		result = res
		return nil
	})
	if txErr != nil {
		if errors.Is(txErr, store.ErrConflict) {
			// Lost an idempotency-key race: the winner already stored the
			// receipt. Replay it when the digest matches.
			if replayed, err := s.replayReceipt(ctx, workspaceID, req, digest); err != nil || replayed != nil {
				return replayed, err
			}
			return nil, txErr
		}
		return nil, txErr
	}
	return result, nil
}

func (s *Service) validateCommitRequest(req CommitRequest) error {
	if strings.TrimSpace(req.RequestID) == "" {
		return fmt.Errorf("%w: request id is required", ErrInvalidOperation)
	}
	if len(req.RequestID) > maxIdentityBytes {
		return fmt.Errorf("%w: request id exceeds %d bytes", ErrLimitExceeded, maxIdentityBytes)
	}
	if strings.TrimSpace(req.DeviceID) == "" {
		return fmt.Errorf("%w: device_id is required", ErrInvalidOperation)
	}
	if len(req.DeviceID) > maxIdentityBytes {
		return fmt.Errorf("%w: device_id exceeds %d bytes", ErrLimitExceeded, maxIdentityBytes)
	}
	if len(req.SessionID) > maxIdentityBytes {
		return fmt.Errorf("%w: session_id exceeds %d bytes", ErrLimitExceeded, maxIdentityBytes)
	}
	if len(req.Note) > s.limits.MaxNoteBytes {
		return fmt.Errorf("%w: note exceeds %d bytes", ErrLimitExceeded, s.limits.MaxNoteBytes)
	}
	if len(req.Operations) == 0 {
		return fmt.Errorf("%w: operations must not be empty", ErrInvalidOperation)
	}
	if len(req.Operations) > s.limits.MaxCommitOperations {
		return fmt.Errorf("%w: more than %d operations", ErrLimitExceeded, s.limits.MaxCommitOperations)
	}
	for i, op := range req.Operations {
		if op.Kind != KindPut && op.Kind != KindDelete {
			return fmt.Errorf("%w: operations[%d] kind %q", ErrInvalidOperation, i, op.Kind)
		}
		if err := ValidatePath(op.Path, s.limits); err != nil {
			return fmt.Errorf("operations[%d]: %w", i, err)
		}
		if IsExcluded(op.Path) {
			return fmt.Errorf("%w: operations[%d] %s", ErrExcludedPath, i, ExcludedReason(op.Path))
		}
		if op.Kind == KindPut && strings.TrimSpace(op.AssetID) == "" {
			return fmt.Errorf("%w: operations[%d] put requires asset_id", ErrInvalidOperation, i)
		}
		if op.Mode > maxModeBits {
			return fmt.Errorf("%w: operations[%d] mode must fit rwx permission bits", ErrInvalidOperation, i)
		}
	}
	return nil
}

// validateReferencedAssets verifies every referenced asset belongs to the
// same tenant, is in a referenceable status and has complete size and
// checksum metadata (doc §8.3, R-03/R-11).
func (s *Service) validateReferencedAssets(ctx context.Context, tenantID string, ops []Operation) (map[string]*store.Asset, error) {
	out := map[string]*store.Asset{}
	for _, id := range referencedAssetIDs(ops) {
		asset, err := s.assets.Get(ctx, tenantID, id)
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("%w: asset %s", ErrInvalidAsset, id)
		}
		if err != nil {
			return nil, err
		}
		if !referenceableStatuses[asset.Status] {
			return nil, fmt.Errorf("%w: asset %s status %q", ErrInvalidAsset, id, asset.Status)
		}
		if asset.Checksum == "" || asset.Bytes <= 0 {
			return nil, fmt.Errorf("%w: asset %s has incomplete checksum or size", ErrInvalidAsset, id)
		}
		if asset.ExpiresAt != nil && !asset.ExpiresAt.After(time.Now()) {
			return nil, fmt.Errorf("%w: asset %s is expired", ErrInvalidAsset, id)
		}
		out[id] = asset
	}
	return out, nil
}

func referencedAssetIDs(ops []Operation) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, op := range ops {
		if op.Kind != KindPut {
			continue
		}
		id := strings.TrimSpace(op.AssetID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// replayReceipt returns the stored result when an idempotent retry is
// detected, or ErrConflictDigest when the key was reused with a different
// body. (nil, nil) means no receipt exists yet.
func (s *Service) replayReceipt(ctx context.Context, workspaceID string, req CommitRequest, digest string) (*CommitResult, error) {
	receipt, err := s.repo.GetReceipt(ctx, workspaceID, req.DeviceID, req.RequestID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if receipt.RequestDigest != digest {
		return nil, ErrConflictDigest
	}
	var res CommitResult
	if err := json.Unmarshal(receipt.Response, &res); err != nil {
		return nil, fmt.Errorf("workspace: decode stored receipt: %w", err)
	}
	res.Response = receipt.Response
	res.Replayed = true
	return &res, nil
}

// applyOperations runs inside the commit transaction. The workspace row is
// already locked; sequences are assigned consecutively starting at
// ws.Sequence+1 in request array order.
func (s *Service) applyOperations(ctx context.Context, ws *Workspace, req CommitRequest, actorID string, assetsByID map[string]*store.Asset) ([]OperationResult, error) {
	now := time.Now().UTC()
	results := make([]OperationResult, 0, len(req.Operations))
	sequence := ws.Sequence
	for i, op := range req.Operations {
		sequence++
		entry, err := s.currentEntry(ctx, ws.ID, op.Path)
		if err != nil {
			return nil, err
		}
		var currentRevisionID string
		if entry != nil {
			currentRevisionID = entry.RevisionID
		}
		switch op.Kind {
		case KindPut:
			if currentRevisionID == op.BaseRevisionID {
				revision := s.newRevision(ws.ID, op.Path, KindPut, op.AssetID, assetsByID[op.AssetID], op.Mode, op.BaseRevisionID, actorID, req, now)
				if err := s.writeRevisionAndChange(ctx, revision, sequence, op.Path, KindPut); err != nil {
					return nil, err
				}
				if err := s.repo.UpsertEntry(ctx, &Entry{WorkspaceID: ws.ID, Path: op.Path, RevisionID: revision.ID, Deleted: false, UpdatedAt: now}); err != nil {
					return nil, err
				}
				results = append(results, OperationResult{
					Index: i, Kind: KindPut, Path: op.Path, Resolution: ResolutionApplied,
					Sequence: sequence, FinalPath: op.Path, Revision: revision.Info(),
				})
				continue
			}
			conflictPath := ConflictPath(op.Path, req.DeviceID, req.RequestID, s.limits)
			revision := s.newRevision(ws.ID, conflictPath, KindPut, op.AssetID, assetsByID[op.AssetID], op.Mode, op.BaseRevisionID, actorID, req, now)
			if err := s.writeRevisionAndChange(ctx, revision, sequence, conflictPath, KindConflict); err != nil {
				return nil, err
			}
			if err := s.repo.UpsertEntry(ctx, &Entry{WorkspaceID: ws.ID, Path: conflictPath, RevisionID: revision.ID, Deleted: false, UpdatedAt: now}); err != nil {
				return nil, err
			}
			results = append(results, OperationResult{
				Index: i, Kind: KindPut, Path: op.Path, Resolution: ResolutionConflict,
				Sequence: sequence, FinalPath: conflictPath, Revision: revision.Info(),
				KeptRevisionID: currentRevisionID,
			})
		case KindDelete:
			if entry != nil && entry.RevisionID == op.BaseRevisionID {
				revision := s.newRevision(ws.ID, op.Path, KindDelete, "", nil, 0, op.BaseRevisionID, actorID, req, now)
				if err := s.writeRevisionAndChange(ctx, revision, sequence, op.Path, KindDelete); err != nil {
					return nil, err
				}
				if err := s.repo.UpsertEntry(ctx, &Entry{WorkspaceID: ws.ID, Path: op.Path, RevisionID: revision.ID, Deleted: true, UpdatedAt: now}); err != nil {
					return nil, err
				}
				results = append(results, OperationResult{
					Index: i, Kind: KindDelete, Path: op.Path, Resolution: ResolutionApplied,
					Sequence: sequence, FinalPath: op.Path, Revision: revision.Info(),
				})
				continue
			}
			// Base mismatch: keep the remote state and record a conflict
			// change (doc §7). The change points at the kept revision so
			// consumers can see what survived.
			change := &Change{WorkspaceID: ws.ID, Sequence: sequence, RevisionID: currentRevisionID, Path: op.Path, Kind: KindConflict, CreatedAt: now}
			if err := s.repo.CreateChange(ctx, change); err != nil {
				return nil, err
			}
			results = append(results, OperationResult{
				Index: i, Kind: KindDelete, Path: op.Path, Resolution: ResolutionConflict,
				Sequence: sequence, FinalPath: op.Path, KeptRevisionID: currentRevisionID,
			})
		}
	}
	return results, nil
}

// currentEntry returns the live entry for path or nil when the path does
// not exist (or is deleted).
func (s *Service) currentEntry(ctx context.Context, workspaceID, p string) (*Entry, error) {
	entry, err := s.repo.GetEntry(ctx, workspaceID, p)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if entry.Deleted {
		return nil, nil
	}
	return entry, nil
}

// writeRevisionAndChange persists one revision and its change row. The
// entry upsert stays at the call site because deleted entries need a
// different tombstone shape.
func (s *Service) writeRevisionAndChange(ctx context.Context, revision *Revision, sequence int64, changePath, changeKind string) error {
	if err := s.repo.CreateRevision(ctx, revision); err != nil {
		return err
	}
	change := &Change{
		WorkspaceID: revision.WorkspaceID,
		Sequence:    sequence,
		RevisionID:  revision.ID,
		Path:        changePath,
		Kind:        changeKind,
		CreatedAt:   revision.CreatedAt,
	}
	return s.repo.CreateChange(ctx, change)
}

func (s *Service) newRevision(workspaceID, p, kind, assetID string, asset *store.Asset, mode uint32, baseRevisionID, actorID string, req CommitRequest, now time.Time) *Revision {
	var bytes int64
	var checksum string
	if asset != nil {
		bytes = asset.Bytes
		checksum = asset.Checksum
	}
	if kind == KindPut && mode == 0 {
		mode = defaultFileMode
	}
	return &Revision{
		ID:             newID(idRevisionPrefix),
		WorkspaceID:    workspaceID,
		Path:           p,
		AssetID:        assetID,
		Kind:           kind,
		Bytes:          bytes,
		Checksum:       checksum,
		Mode:           mode,
		BaseRevisionID: baseRevisionID,
		ActorID:        actorID,
		DeviceID:       req.DeviceID,
		SessionID:      req.SessionID,
		Note:           req.Note,
		CreatedAt:      now,
	}
}

// --- Restore -------------------------------------------------------------

// Restore creates a new put revision that copies the target revision's
// asset reference; history is never rewritten (doc §8.5). The target
// revision must belong to the same path's history.
func (s *Service) Restore(ctx context.Context, tenantID, workspaceID, p, revisionID, note, actorID, deviceID, sessionID string) (*RestoreResult, error) {
	if err := ValidatePath(p, s.limits); err != nil {
		return nil, err
	}
	if IsExcluded(p) {
		return nil, fmt.Errorf("%w: %s", ErrExcludedPath, ExcludedReason(p))
	}
	if len(note) > s.limits.MaxNoteBytes {
		return nil, fmt.Errorf("%w: note exceeds %d bytes", ErrLimitExceeded, s.limits.MaxNoteBytes)
	}
	if _, err := s.activeWorkspace(ctx, tenantID, workspaceID); err != nil {
		return nil, err
	}
	target, err := s.repo.GetRevision(ctx, workspaceID, revisionID)
	if err != nil {
		return nil, err
	}
	if target.Path != p {
		return nil, fmt.Errorf("%w: revision does not belong to path history", ErrInvalidOperation)
	}
	if target.Kind != KindPut {
		return nil, fmt.Errorf("%w: only put revisions can be restored", ErrInvalidOperation)
	}
	if _, err := s.assets.Get(ctx, tenantID, target.AssetID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("%w: asset %s", ErrInvalidAsset, target.AssetID)
		}
		return nil, err
	}

	var result *RestoreResult
	txErr := s.repo.WithTx(ctx, func(ctx context.Context) error {
		ws, err := s.repo.LockWorkspace(ctx, tenantID, workspaceID)
		if err != nil {
			return err
		}
		if ws.DeletedAt != nil {
			return ErrGone
		}
		entry, err := s.currentEntry(ctx, workspaceID, p)
		if err != nil {
			return err
		}
		baseRevisionID := ""
		if entry != nil {
			baseRevisionID = entry.RevisionID
		}
		now := time.Now().UTC()
		sequence := ws.Sequence + 1
		revision := &Revision{
			ID:             newID(idRevisionPrefix),
			WorkspaceID:    workspaceID,
			Path:           p,
			AssetID:        target.AssetID,
			Kind:           KindPut,
			Bytes:          target.Bytes,
			Checksum:       target.Checksum,
			Mode:           target.Mode,
			BaseRevisionID: baseRevisionID,
			ActorID:        actorID,
			DeviceID:       deviceID,
			SessionID:      sessionID,
			Note:           note,
			CreatedAt:      now,
		}
		if err := s.writeRevisionAndChange(ctx, revision, sequence, p, KindPut); err != nil {
			return err
		}
		if err := s.repo.UpsertEntry(ctx, &Entry{WorkspaceID: workspaceID, Path: p, RevisionID: revision.ID, Deleted: false, UpdatedAt: now}); err != nil {
			return err
		}
		if err := s.repo.UpdateWorkspaceSequence(ctx, workspaceID, sequence); err != nil {
			return err
		}
		if err := s.repo.ClearAssetExpiry(ctx, []string{target.AssetID}); err != nil {
			return err
		}
		result = &RestoreResult{Object: "restore", Path: p, RevisionID: revision.ID, AssetID: target.AssetID, Bytes: target.Bytes, Sequence: sequence}
		return nil
	})
	if txErr != nil {
		return nil, txErr
	}
	return result, nil
}

// --- Shares --------------------------------------------------------------

// ShareTokenBytes is the entropy of a share token.
const ShareTokenBytes = 32

// shareTokenPrefixLen is how many token characters list responses may
// show as a hint.
const shareTokenPrefixLen = 8

// CreateShare mints a new share link for a live path. The plain token is
// returned exactly once; only its SHA-256 hash is persisted (SEC-04).
func (s *Service) CreateShare(ctx context.Context, tenantID, workspaceID, p, creatorID string, ttl time.Duration) (*ShareLink, string, error) {
	if err := ValidatePath(p, s.limits); err != nil {
		return nil, "", err
	}
	if _, err := s.activeWorkspace(ctx, tenantID, workspaceID); err != nil {
		return nil, "", err
	}
	view, err := s.GetEntry(ctx, tenantID, workspaceID, p)
	if err != nil {
		return nil, "", err
	}
	token, err := generateShareToken()
	if err != nil {
		return nil, "", err
	}
	share := &ShareLink{
		ID:          newID(idSharePrefix),
		TokenHash:   HashShareToken(token),
		TokenPrefix: token[:shareTokenPrefixLen],
		WorkspaceID: workspaceID,
		Path:        view.Path,
		CreatorID:   creatorID,
		CreatedAt:   time.Now().UTC(),
	}
	if ttl > 0 {
		expires := share.CreatedAt.Add(ttl)
		share.ExpiresAt = &expires
	}
	if err := s.repo.CreateShare(ctx, share); err != nil {
		return nil, "", err
	}
	return share, token, nil
}

// ListShares returns share metadata plus the token prefix hint; the full
// token is never returned again (doc §8.5, SEC-04).
func (s *Service) ListShares(ctx context.Context, tenantID, workspaceID string, offset, limit int) ([]*ShareLink, bool, error) {
	if _, err := s.activeWorkspace(ctx, tenantID, workspaceID); err != nil {
		return nil, false, err
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.ListShares(ctx, workspaceID, offset, ClampListLimit(limit))
}

// RevokeShare revokes a share by its share ID; revocation is immediate
// (SEC-04). The token itself is never required or accepted here.
func (s *Service) RevokeShare(ctx context.Context, tenantID, workspaceID, shareID string) error {
	if _, err := s.repo.GetWorkspace(ctx, tenantID, workspaceID); err != nil {
		return err
	}
	revoked, err := s.repo.RevokeShare(ctx, workspaceID, shareID, time.Now().UTC())
	if err != nil {
		return err
	}
	if !revoked {
		return ErrNotFound
	}
	return nil
}

// ResolveShare maps a public token to the current asset for the shared
// path. Invalid, revoked or expired tokens yield ErrNotFound without
// distinguishing reasons (anti-enumeration, doc §8.5); a soft-deleted
// workspace yields ErrGone (410).
func (s *Service) ResolveShare(ctx context.Context, token string) (*ShareResolution, error) {
	if !validShareToken(token) {
		return nil, ErrNotFound
	}
	share, ws, err := s.repo.GetShareByTokenHash(ctx, HashShareToken(token))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if ws == nil || ws.DeletedAt != nil {
		return nil, ErrGone
	}
	if share.RevokedAt != nil {
		return nil, ErrNotFound
	}
	if share.ExpiresAt != nil && !share.ExpiresAt.After(time.Now()) {
		return nil, ErrNotFound
	}
	entry, err := s.repo.GetEntry(ctx, ws.ID, share.Path)
	if err != nil || entry.Deleted {
		return nil, ErrNotFound
	}
	revision, err := s.repo.GetRevision(ctx, ws.ID, entry.RevisionID)
	if err != nil || revision.AssetID == "" {
		return nil, ErrNotFound
	}
	return &ShareResolution{WorkspaceID: ws.ID, TenantID: ws.TenantID, Path: share.Path, AssetID: revision.AssetID}, nil
}

// --- Read events ---------------------------------------------------------

// ReadEventInput is one client-reported read event.
type ReadEventInput struct {
	Path      string
	Kind      string
	SessionID string
	DeviceID  string
	Count     int64
}

// RecordReadEvents aggregates events into UTC-day rows and returns the
// cumulative per-path totals covering all recorded days.
func (s *Service) RecordReadEvents(ctx context.Context, tenantID, workspaceID, actorID string, inputs []ReadEventInput) ([]ReadStat, error) {
	if _, err := s.activeWorkspace(ctx, tenantID, workspaceID); err != nil {
		return nil, err
	}
	if len(inputs) == 0 {
		return nil, fmt.Errorf("%w: events must not be empty", ErrInvalidOperation)
	}
	if len(inputs) > s.limits.MaxReadEventBatch {
		return nil, fmt.Errorf("%w: more than %d read events", ErrLimitExceeded, s.limits.MaxReadEventBatch)
	}
	now := time.Now().UTC()
	day := now.Format("2006-01-02")
	events := make([]*ReadEvent, 0, len(inputs))
	paths := make([]string, 0, len(inputs))
	seen := map[string]bool{}
	for i, in := range inputs {
		if err := ValidatePath(in.Path, s.limits); err != nil {
			return nil, fmt.Errorf("events[%d]: %w", i, err)
		}
		if IsExcluded(in.Path) {
			return nil, fmt.Errorf("%w: events[%d] %s", ErrExcludedPath, i, ExcludedReason(in.Path))
		}
		if in.Kind != ReadKindHuman && in.Kind != ReadKindAgent && in.Kind != ReadKindShare {
			return nil, fmt.Errorf("%w: events[%d] kind %q", ErrInvalidOperation, i, in.Kind)
		}
		if in.Count <= 0 || in.Count > 1000000 {
			return nil, fmt.Errorf("%w: events[%d] count must be between 1 and 1000000", ErrInvalidOperation, i)
		}
		events = append(events, &ReadEvent{
			WorkspaceID: workspaceID,
			Path:        in.Path,
			Kind:        in.Kind,
			ActorID:     actorID,
			DeviceID:    in.DeviceID,
			SessionID:   in.SessionID,
			Day:         day,
			Count:       in.Count,
			LastReadAt:  now,
		})
		if !seen[in.Path] {
			seen[in.Path] = true
			paths = append(paths, in.Path)
		}
	}
	if err := s.repo.RecordReadEvents(ctx, events); err != nil {
		return nil, err
	}
	totals, err := s.repo.ReadTotalsByPath(ctx, workspaceID, paths)
	if err != nil {
		return nil, err
	}
	out := make([]ReadStat, 0, len(paths))
	for _, p := range paths {
		out = append(out, ReadStat{Path: p, Count: totals[p]})
	}
	return out, nil
}

// ReadStats aggregates read counts by path and UTC day for the requested
// window (doc §8.5). No actor identity is exposed.
func (s *Service) ReadStats(ctx context.Context, tenantID, workspaceID, prefix string, days int) ([]ReadStat, error) {
	if _, err := s.activeWorkspace(ctx, tenantID, workspaceID); err != nil {
		return nil, err
	}
	if days <= 0 {
		days = 30
	}
	if days > 365 {
		days = 365
	}
	sinceDay := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	return s.repo.ReadStats(ctx, workspaceID, prefix, sinceDay)
}

// --- Asset reference protection ------------------------------------------

// AssetReferenced implements AssetReferenceChecker. The tenant parameter
// keeps the checker interface stable; cross-tenant references cannot
// exist because commits validate asset tenant ownership server-side.
func (s *Service) AssetReferenced(ctx context.Context, tenantID, assetID string) (bool, error) {
	return s.repo.AssetReferenced(ctx, assetID)
}
