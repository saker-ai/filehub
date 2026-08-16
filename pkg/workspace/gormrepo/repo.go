package gormrepo

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/saker-ai/filehub/pkg/store"
	"github.com/saker-ai/filehub/pkg/workspace"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type txKey struct{}

// Repo implements workspace.Repository on a shared GORM handle.
type Repo struct {
	db *gorm.DB
}

// New constructs a Repo and migrates the workspace tables. The handle is
// expected to come from gormstore.Store.DB so both subsystems share one
// pool and one database.
func New(ctx context.Context, db *gorm.DB) (*Repo, error) {
	if db == nil {
		return nil, errors.New("gormrepo: nil database handle")
	}
	err := db.WithContext(ctx).AutoMigrate(
		&WorkspaceModel{},
		&EntryModel{},
		&RevisionModel{},
		&ChangeModel{},
		&ReceiptModel{},
		&ShareLinkModel{},
		&ReadEventModel{},
	)
	if err != nil {
		return nil, fmt.Errorf("gormrepo: migrate: %w", err)
	}
	return &Repo{db: db}, nil
}

// conn returns the transaction stored in ctx when present, otherwise the
// plain handle. Every query goes through this helper so WithTx composes one
// atomic unit (doc §9, Repository.WithTx contract).
func (r *Repo) conn(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(txKey{}).(*gorm.DB); ok {
		return tx.WithContext(ctx)
	}
	return r.db.WithContext(ctx)
}

// WithTx implements workspace.TxRunner.
func (r *Repo) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
}

// --- Workspaces -----------------------------------------------------------

func workspaceToModel(ws *workspace.Workspace) *WorkspaceModel {
	return &WorkspaceModel{
		ID:          ws.ID,
		TenantID:    ws.TenantID,
		Name:        ws.Name,
		Description: ws.Description,
		Sequence:    ws.Sequence,
		CreatedAt:   ws.CreatedAt,
		UpdatedAt:   ws.UpdatedAt,
		DeletedAt:   ws.DeletedAt,
	}
}

func modelToWorkspace(m *WorkspaceModel) *workspace.Workspace {
	return &workspace.Workspace{
		ID:          m.ID,
		TenantID:    m.TenantID,
		Name:        m.Name,
		Description: m.Description,
		Sequence:    m.Sequence,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
		DeletedAt:   m.DeletedAt,
	}
}

// CreateWorkspace implements workspace.WorkspaceRepo.
func (r *Repo) CreateWorkspace(ctx context.Context, ws *workspace.Workspace) error {
	if err := r.conn(ctx).Create(workspaceToModel(ws)).Error; err != nil {
		return fmt.Errorf("gormrepo: create workspace: %w", err)
	}
	return nil
}

// GetWorkspace implements workspace.WorkspaceRepo.
func (r *Repo) GetWorkspace(ctx context.Context, tenantID, id string) (*workspace.Workspace, error) {
	var m WorkspaceModel
	err := r.conn(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, workspace.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("gormrepo: get workspace: %w", err)
	}
	return modelToWorkspace(&m), nil
}

// LockWorkspace implements workspace.WorkspaceRepo. Postgres takes a row
// lock; SQLite serializes writers on its single connection, so the plain
// read is sufficient there.
func (r *Repo) LockWorkspace(ctx context.Context, tenantID, id string) (*workspace.Workspace, error) {
	q := r.conn(ctx).Where("tenant_id = ? AND id = ?", tenantID, id)
	if r.db.Dialector.Name() == "postgres" {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var m WorkspaceModel
	err := q.First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, workspace.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("gormrepo: lock workspace: %w", err)
	}
	return modelToWorkspace(&m), nil
}

// ListWorkspaces implements workspace.WorkspaceRepo: newest first, cursor is
// "createdAtUnixNano:id" of the last item of the previous page.
func (r *Repo) ListWorkspaces(ctx context.Context, tenantID, cursor string, limit int) ([]*workspace.Workspace, bool, error) {
	q := r.conn(ctx).Where("tenant_id = ?", tenantID).Order("created_at DESC, id DESC")
	if cursor != "" {
		createdAt, id, err := parseTimeIDCursor(cursor)
		if err != nil {
			return nil, false, workspace.ErrNotFound
		}
		q = q.Where("(created_at < ? OR (created_at = ? AND id < ?))", createdAt, createdAt, id)
	}
	var models []WorkspaceModel
	if err := q.Limit(limit + 1).Find(&models).Error; err != nil {
		return nil, false, fmt.Errorf("gormrepo: list workspaces: %w", err)
	}
	hasMore := len(models) > limit
	if hasMore {
		models = models[:limit]
	}
	out := make([]*workspace.Workspace, 0, len(models))
	for i := range models {
		out = append(out, modelToWorkspace(&models[i]))
	}
	return out, hasMore, nil
}

// UpdateWorkspace implements workspace.WorkspaceRepo (name/description).
func (r *Repo) UpdateWorkspace(ctx context.Context, ws *workspace.Workspace) error {
	ws.UpdatedAt = time.Now().UTC()
	res := r.conn(ctx).Model(&WorkspaceModel{}).
		Where("tenant_id = ? AND id = ?", ws.TenantID, ws.ID).
		Updates(map[string]any{"name": ws.Name, "description": ws.Description, "updated_at": ws.UpdatedAt})
	if res.Error != nil {
		return fmt.Errorf("gormrepo: update workspace: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return workspace.ErrNotFound
	}
	return nil
}

// UpdateWorkspaceSequence implements workspace.WorkspaceRepo. Sequence only
// moves forward.
func (r *Repo) UpdateWorkspaceSequence(ctx context.Context, workspaceID string, sequence int64) error {
	res := r.conn(ctx).Model(&WorkspaceModel{}).
		Where("id = ? AND sequence < ?", workspaceID, sequence).
		Updates(map[string]any{"sequence": sequence, "updated_at": time.Now().UTC()})
	if res.Error != nil {
		return fmt.Errorf("gormrepo: update sequence: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("gormrepo: sequence did not advance: %w", workspace.ErrNotFound)
	}
	return nil
}

// SoftDeleteWorkspace implements workspace.WorkspaceRepo.
func (r *Repo) SoftDeleteWorkspace(ctx context.Context, tenantID, id string) error {
	now := time.Now().UTC()
	res := r.conn(ctx).Model(&WorkspaceModel{}).
		Where("tenant_id = ? AND id = ? AND deleted_at IS NULL", tenantID, id).
		Updates(map[string]any{"deleted_at": now, "updated_at": now})
	if res.Error != nil {
		return fmt.Errorf("gormrepo: soft delete workspace: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return workspace.ErrNotFound
	}
	return nil
}

// --- Entries ---------------------------------------------------------------

// GetEntry implements workspace.EntryRepo.
func (r *Repo) GetEntry(ctx context.Context, workspaceID, path string) (*workspace.Entry, error) {
	var m EntryModel
	err := r.conn(ctx).Where("workspace_id = ? AND path = ?", workspaceID, path).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, workspace.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("gormrepo: get entry: %w", err)
	}
	return &workspace.Entry{WorkspaceID: m.WorkspaceID, Path: m.Path, RevisionID: m.RevisionID, Deleted: m.Deleted, UpdatedAt: m.UpdatedAt}, nil
}

// UpsertEntry implements workspace.EntryRepo.
func (r *Repo) UpsertEntry(ctx context.Context, entry *workspace.Entry) error {
	m := EntryModel{WorkspaceID: entry.WorkspaceID, Path: entry.Path, RevisionID: entry.RevisionID, Deleted: entry.Deleted, UpdatedAt: entry.UpdatedAt}
	if err := r.conn(ctx).Save(&m).Error; err != nil {
		return fmt.Errorf("gormrepo: upsert entry: %w", err)
	}
	return nil
}

// ListEntries implements workspace.EntryRepo: live entries, path ascending,
// optional lexicographic prefix and cursor (last path of previous page).
func (r *Repo) ListEntries(ctx context.Context, workspaceID, prefix, cursor string, limit int) ([]*workspace.Entry, bool, error) {
	q := r.conn(ctx).Where("workspace_id = ? AND deleted = ?", workspaceID, false)
	if prefix != "" {
		lo, hi := prefixRange(prefix)
		if hi == "" {
			q = q.Where("path >= ?", lo)
		} else {
			q = q.Where("path >= ? AND path < ?", lo, hi)
		}
	}
	if cursor != "" {
		q = q.Where("path > ?", cursor)
	}
	var models []EntryModel
	if err := q.Order("path ASC").Limit(limit + 1).Find(&models).Error; err != nil {
		return nil, false, fmt.Errorf("gormrepo: list entries: %w", err)
	}
	hasMore := len(models) > limit
	if hasMore {
		models = models[:limit]
	}
	out := make([]*workspace.Entry, 0, len(models))
	for i := range models {
		m := &models[i]
		out = append(out, &workspace.Entry{WorkspaceID: m.WorkspaceID, Path: m.Path, RevisionID: m.RevisionID, Deleted: m.Deleted, UpdatedAt: m.UpdatedAt})
	}
	return out, hasMore, nil
}

// --- Revisions --------------------------------------------------------------

func revisionToModel(rev *workspace.Revision) *RevisionModel {
	return &RevisionModel{
		ID:             rev.ID,
		WorkspaceID:    rev.WorkspaceID,
		Path:           rev.Path,
		AssetID:        rev.AssetID,
		Kind:           rev.Kind,
		Bytes:          rev.Bytes,
		Checksum:       rev.Checksum,
		Mode:           rev.Mode,
		BaseRevisionID: rev.BaseRevisionID,
		ActorID:        rev.ActorID,
		DeviceID:       rev.DeviceID,
		SessionID:      rev.SessionID,
		Note:           rev.Note,
		CreatedAt:      rev.CreatedAt,
	}
}

func modelToRevision(m *RevisionModel) *workspace.Revision {
	return &workspace.Revision{
		ID:             m.ID,
		WorkspaceID:    m.WorkspaceID,
		Path:           m.Path,
		AssetID:        m.AssetID,
		Kind:           m.Kind,
		Bytes:          m.Bytes,
		Checksum:       m.Checksum,
		Mode:           m.Mode,
		BaseRevisionID: m.BaseRevisionID,
		ActorID:        m.ActorID,
		DeviceID:       m.DeviceID,
		SessionID:      m.SessionID,
		Note:           m.Note,
		CreatedAt:      m.CreatedAt,
	}
}

// CreateRevision implements workspace.RevisionRepo.
func (r *Repo) CreateRevision(ctx context.Context, rev *workspace.Revision) error {
	if err := r.conn(ctx).Create(revisionToModel(rev)).Error; err != nil {
		return fmt.Errorf("gormrepo: create revision: %w", err)
	}
	return nil
}

// GetRevision implements workspace.RevisionRepo.
func (r *Repo) GetRevision(ctx context.Context, workspaceID, id string) (*workspace.Revision, error) {
	var m RevisionModel
	err := r.conn(ctx).Where("workspace_id = ? AND id = ?", workspaceID, id).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, workspace.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("gormrepo: get revision: %w", err)
	}
	return modelToRevision(&m), nil
}

// GetRevisions implements workspace.RevisionRepo.
func (r *Repo) GetRevisions(ctx context.Context, workspaceID string, ids []string) ([]*workspace.Revision, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var models []RevisionModel
	if err := r.conn(ctx).Where("workspace_id = ? AND id IN ?", workspaceID, ids).Find(&models).Error; err != nil {
		return nil, fmt.Errorf("gormrepo: get revisions: %w", err)
	}
	out := make([]*workspace.Revision, 0, len(models))
	for i := range models {
		out = append(out, modelToRevision(&models[i]))
	}
	return out, nil
}

// ListRevisions implements workspace.RevisionRepo: newest first, optional
// path filter, cursor is "createdAtUnixNano:id".
func (r *Repo) ListRevisions(ctx context.Context, workspaceID, path, cursor string, limit int) ([]*workspace.Revision, bool, error) {
	q := r.conn(ctx).Where("workspace_id = ?", workspaceID).Order("created_at DESC, id DESC")
	if path != "" {
		q = q.Where("path = ?", path)
	}
	if cursor != "" {
		createdAt, id, err := parseTimeIDCursor(cursor)
		if err != nil {
			return nil, false, workspace.ErrNotFound
		}
		q = q.Where("(created_at < ? OR (created_at = ? AND id < ?))", createdAt, createdAt, id)
	}
	var models []RevisionModel
	if err := q.Limit(limit + 1).Find(&models).Error; err != nil {
		return nil, false, fmt.Errorf("gormrepo: list revisions: %w", err)
	}
	hasMore := len(models) > limit
	if hasMore {
		models = models[:limit]
	}
	out := make([]*workspace.Revision, 0, len(models))
	for i := range models {
		out = append(out, modelToRevision(&models[i]))
	}
	return out, hasMore, nil
}

// AssetReferenced implements workspace.RevisionRepo (FH-11).
func (r *Repo) AssetReferenced(ctx context.Context, assetID string) (bool, error) {
	var count int64
	err := r.conn(ctx).Model(&RevisionModel{}).Where("asset_id = ?", assetID).Limit(1).Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("gormrepo: asset referenced: %w", err)
	}
	return count > 0, nil
}

// ClearAssetExpiry implements workspace.RevisionRepo: referenced assets lose
// their expiry eligibility inside the commit transaction (doc §8.3).
func (r *Repo) ClearAssetExpiry(ctx context.Context, assetIDs []string) error {
	if len(assetIDs) == 0 {
		return nil
	}
	err := r.conn(ctx).Table("assets").Where("id IN ?", assetIDs).Update("expires_at", nil).Error
	if err != nil {
		return fmt.Errorf("gormrepo: clear asset expiry: %w", err)
	}
	return nil
}

// --- Changes ----------------------------------------------------------------

// CreateChange implements workspace.ChangeRepo.
func (r *Repo) CreateChange(ctx context.Context, ch *workspace.Change) error {
	m := ChangeModel{WorkspaceID: ch.WorkspaceID, Sequence: ch.Sequence, RevisionID: ch.RevisionID, Path: ch.Path, Kind: ch.Kind, CreatedAt: ch.CreatedAt}
	if err := r.conn(ctx).Create(&m).Error; err != nil {
		return fmt.Errorf("gormrepo: create change: %w", err)
	}
	return nil
}

// ListChanges implements workspace.ChangeRepo: sequences strictly greater
// than after, ascending.
func (r *Repo) ListChanges(ctx context.Context, workspaceID string, after int64, limit int) ([]*workspace.Change, bool, error) {
	var models []ChangeModel
	err := r.conn(ctx).
		Where("workspace_id = ? AND sequence > ?", workspaceID, after).
		Order("sequence ASC").
		Limit(limit + 1).
		Find(&models).Error
	if err != nil {
		return nil, false, fmt.Errorf("gormrepo: list changes: %w", err)
	}
	hasMore := len(models) > limit
	if hasMore {
		models = models[:limit]
	}
	out := make([]*workspace.Change, 0, len(models))
	for i := range models {
		m := &models[i]
		out = append(out, &workspace.Change{WorkspaceID: m.WorkspaceID, Sequence: m.Sequence, RevisionID: m.RevisionID, Path: m.Path, Kind: m.Kind, CreatedAt: m.CreatedAt})
	}
	return out, hasMore, nil
}

// --- Receipts ----------------------------------------------------------------

// GetReceipt implements workspace.ReceiptRepo.
func (r *Repo) GetReceipt(ctx context.Context, workspaceID, deviceID, requestID string) (*workspace.CommitReceipt, error) {
	var m ReceiptModel
	err := r.conn(ctx).
		Where("workspace_id = ? AND device_id = ? AND request_id = ?", workspaceID, deviceID, requestID).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, workspace.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("gormrepo: get receipt: %w", err)
	}
	return &workspace.CommitReceipt{
		WorkspaceID:   m.WorkspaceID,
		DeviceID:      m.DeviceID,
		RequestID:     m.RequestID,
		RequestDigest: m.RequestDigest,
		FromSequence:  m.FromSequence,
		ToSequence:    m.ToSequence,
		Response:      []byte(m.Response),
		CreatedAt:     m.CreatedAt,
	}, nil
}

// CreateReceipt implements workspace.ReceiptRepo. A duplicate key means the
// caller lost an idempotency race; it surfaces as store.ErrConflict so the
// service can replay the winner's stored receipt (doc §8.3, FH-03/FH-04).
func (r *Repo) CreateReceipt(ctx context.Context, receipt *workspace.CommitReceipt) error {
	m := ReceiptModel{
		WorkspaceID:   receipt.WorkspaceID,
		DeviceID:      receipt.DeviceID,
		RequestID:     receipt.RequestID,
		RequestDigest: receipt.RequestDigest,
		FromSequence:  receipt.FromSequence,
		ToSequence:    receipt.ToSequence,
		Response:      string(receipt.Response),
		CreatedAt:     receipt.CreatedAt,
	}
	if err := r.conn(ctx).Create(&m).Error; err != nil {
		if isUniqueConstraintError(err) {
			return store.ErrConflict
		}
		return fmt.Errorf("gormrepo: create receipt: %w", err)
	}
	return nil
}

func isUniqueConstraintError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "duplicate entry") ||
		strings.Contains(msg, "uniq constraint")
}

// --- Shares -------------------------------------------------------------------

// CreateShare implements workspace.ShareRepo.
func (r *Repo) CreateShare(ctx context.Context, share *workspace.ShareLink) error {
	m := ShareLinkModel{
		ID:          share.ID,
		TokenHash:   share.TokenHash,
		TokenPrefix: share.TokenPrefix,
		WorkspaceID: share.WorkspaceID,
		Path:        share.Path,
		CreatorID:   share.CreatorID,
		ExpiresAt:   share.ExpiresAt,
		RevokedAt:   share.RevokedAt,
		CreatedAt:   share.CreatedAt,
	}
	if err := r.conn(ctx).Create(&m).Error; err != nil {
		return fmt.Errorf("gormrepo: create share: %w", err)
	}
	return nil
}

// GetShare implements workspace.ShareRepo.
func (r *Repo) GetShare(ctx context.Context, workspaceID, shareID string) (*workspace.ShareLink, error) {
	var m ShareLinkModel
	err := r.conn(ctx).Where("workspace_id = ? AND id = ?", workspaceID, shareID).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, workspace.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("gormrepo: get share: %w", err)
	}
	return modelToShare(&m), nil
}

func modelToShare(m *ShareLinkModel) *workspace.ShareLink {
	return &workspace.ShareLink{
		ID:          m.ID,
		TokenHash:   m.TokenHash,
		TokenPrefix: m.TokenPrefix,
		WorkspaceID: m.WorkspaceID,
		Path:        m.Path,
		CreatorID:   m.CreatorID,
		ExpiresAt:   m.ExpiresAt,
		RevokedAt:   m.RevokedAt,
		CreatedAt:   m.CreatedAt,
	}
}

// GetShareByTokenHash implements workspace.ShareRepo: the share plus its
// workspace so callers can evaluate soft-deletion in one step.
func (r *Repo) GetShareByTokenHash(ctx context.Context, tokenHash string) (*workspace.ShareLink, *workspace.Workspace, error) {
	var m ShareLinkModel
	err := r.conn(ctx).Where("token_hash = ?", tokenHash).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, workspace.ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("gormrepo: get share by token: %w", err)
	}
	var wm WorkspaceModel
	err = r.conn(ctx).Where("id = ?", m.WorkspaceID).First(&wm).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, workspace.ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("gormrepo: get share workspace: %w", err)
	}
	return modelToShare(&m), modelToWorkspace(&wm), nil
}

// ListShares implements workspace.ShareRepo: newest first with offset
// pagination (share lists are small; offset keeps the contract simple).
func (r *Repo) ListShares(ctx context.Context, workspaceID string, offset, limit int) ([]*workspace.ShareLink, bool, error) {
	var models []ShareLinkModel
	err := r.conn(ctx).
		Where("workspace_id = ?", workspaceID).
		Order("created_at DESC, id DESC").
		Offset(offset).
		Limit(limit + 1).
		Find(&models).Error
	if err != nil {
		return nil, false, fmt.Errorf("gormrepo: list shares: %w", err)
	}
	hasMore := len(models) > limit
	if hasMore {
		models = models[:limit]
	}
	out := make([]*workspace.ShareLink, 0, len(models))
	for i := range models {
		out = append(out, modelToShare(&models[i]))
	}
	return out, hasMore, nil
}

// RevokeShare implements workspace.ShareRepo.
func (r *Repo) RevokeShare(ctx context.Context, workspaceID, shareID string, at time.Time) (bool, error) {
	res := r.conn(ctx).Model(&ShareLinkModel{}).
		Where("workspace_id = ? AND id = ? AND revoked_at IS NULL", workspaceID, shareID).
		Update("revoked_at", at)
	if res.Error != nil {
		return false, fmt.Errorf("gormrepo: revoke share: %w", res.Error)
	}
	return res.RowsAffected > 0, nil
}

// --- Read events ---------------------------------------------------------------

// RecordReadEvents implements workspace.ReadEventRepo. Each event atomically
// increments its UTC-day row inside one transaction.
func (r *Repo) RecordReadEvents(ctx context.Context, events []*workspace.ReadEvent) error {
	if len(events) == 0 {
		return nil
	}
	return r.WithTx(ctx, func(ctx context.Context) error {
		for _, ev := range events {
			var m ReadEventModel
			err := r.conn(ctx).
				Where("workspace_id = ? AND path = ? AND kind = ? AND day = ?", ev.WorkspaceID, ev.Path, ev.Kind, ev.Day).
				First(&m).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				row := ReadEventModel{
					WorkspaceID: ev.WorkspaceID, Path: ev.Path, Kind: ev.Kind, Day: ev.Day,
					ActorID: ev.ActorID, DeviceID: ev.DeviceID, SessionID: ev.SessionID,
					Count: ev.Count, LastReadAt: ev.LastReadAt,
				}
				if err := r.conn(ctx).Create(&row).Error; err != nil {
					return fmt.Errorf("gormrepo: record read event: %w", err)
				}
				continue
			}
			if err != nil {
				return fmt.Errorf("gormrepo: record read event: %w", err)
			}
			res := r.conn(ctx).Model(&ReadEventModel{}).
				Where("workspace_id = ? AND path = ? AND kind = ? AND day = ?", ev.WorkspaceID, ev.Path, ev.Kind, ev.Day).
				Updates(map[string]any{
					"count":        m.Count + ev.Count,
					"last_read_at": ev.LastReadAt,
					"actor_id":     ev.ActorID,
					"device_id":    ev.DeviceID,
					"session_id":   ev.SessionID,
				})
			if res.Error != nil {
				return fmt.Errorf("gormrepo: record read event: %w", res.Error)
			}
		}
		return nil
	})
}

// ReadTotalsByPath implements workspace.ReadEventRepo.
func (r *Repo) ReadTotalsByPath(ctx context.Context, workspaceID string, paths []string) (map[string]int64, error) {
	out := map[string]int64{}
	if len(paths) == 0 {
		return out, nil
	}
	type row struct {
		Path  string
		Total int64
	}
	var rows []row
	err := r.conn(ctx).Model(&ReadEventModel{}).
		Select("path, SUM(count) AS total").
		Where("workspace_id = ? AND path IN ?", workspaceID, paths).
		Group("path").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("gormrepo: read totals: %w", err)
	}
	for _, r := range rows {
		out[r.Path] = r.Total
	}
	return out, nil
}

// ReadStats implements workspace.ReadEventRepo: per path/day aggregates
// since the given UTC day, ascending.
func (r *Repo) ReadStats(ctx context.Context, workspaceID, prefix string, sinceDay string) ([]workspace.ReadStat, error) {
	q := r.conn(ctx).Model(&ReadEventModel{}).
		Select("path, day, SUM(count) AS count").
		Where("workspace_id = ? AND day >= ?", workspaceID, sinceDay)
	if prefix != "" {
		lo, hi := prefixRange(prefix)
		if hi == "" {
			q = q.Where("path >= ?", lo)
		} else {
			q = q.Where("path >= ? AND path < ?", lo, hi)
		}
	}
	type row struct {
		Path  string
		Day   string
		Count int64
	}
	var rows []row
	if err := q.Group("path, day").Order("day ASC, path ASC").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("gormrepo: read stats: %w", err)
	}
	out := make([]workspace.ReadStat, 0, len(rows))
	for _, r := range rows {
		out = append(out, workspace.ReadStat{Path: r.Path, Day: r.Day, Count: r.Count})
	}
	return out, nil
}

// --- cursor helpers -------------------------------------------------------------

// parseTimeIDCursor parses "unixNano:id" cursors used by newest-first lists.
func parseTimeIDCursor(cursor string) (time.Time, string, error) {
	idx := strings.IndexByte(cursor, ':')
	if idx <= 0 || idx == len(cursor)-1 {
		return time.Time{}, "", fmt.Errorf("gormrepo: invalid cursor")
	}
	nanos, err := strconv.ParseInt(cursor[:idx], 10, 64)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("gormrepo: invalid cursor: %w", err)
	}
	return time.Unix(0, nanos).UTC(), cursor[idx+1:], nil
}

// TimeIDCursor builds the "unixNano:id" cursor for one row.
func TimeIDCursor(createdAt time.Time, id string) string {
	return fmt.Sprintf("%d:%s", createdAt.UnixNano(), id)
}

// prefixRange returns the [lo, hi) key range matching all strings with the
// given prefix, avoiding LIKE escaping pitfalls. hi is empty when the
// prefix cannot be incremented (all 0xFF bytes).
func prefixRange(prefix string) (lo, hi string) {
	lo = prefix
	bytes := []byte(prefix)
	for i := len(bytes) - 1; i >= 0; i-- {
		if bytes[i] < 0xFF {
			up := make([]byte, i+1)
			copy(up, bytes[:i+1])
			up[i]++
			return lo, string(up)
		}
	}
	return lo, ""
}
