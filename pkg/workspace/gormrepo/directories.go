package gormrepo

import (
	"context"
	"fmt"

	"github.com/saker-ai/filehub/pkg/workspace"
	"gorm.io/gorm/clause"
)

func (r *Repo) SaveDirectory(ctx context.Context, dir *workspace.Directory) error {
	row := DirectoryModel{WorkspaceID: dir.WorkspaceID, Path: dir.Path, CreatedAt: dir.CreatedAt}
	if err := r.conn(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return fmt.Errorf("gormrepo: save directory: %w", err)
	}
	return nil
}

func (r *Repo) DeleteDirectory(ctx context.Context, workspaceID, directoryPath string) error {
	if err := r.conn(ctx).Where("workspace_id = ? AND path = ?", workspaceID, directoryPath).Delete(&DirectoryModel{}).Error; err != nil {
		return fmt.Errorf("gormrepo: delete directory: %w", err)
	}
	return nil
}

func (r *Repo) DirectoryExists(ctx context.Context, workspaceID, p string) (bool, error) {
	lo, hi := prefixRange(p + "/")
	var matches []string
	err := r.conn(ctx).Model(&DirectoryModel{}).Where("workspace_id = ?", workspaceID).
		Where("path = ? OR (path >= ? AND path < ?)", p, lo, hi).Limit(1).Pluck("path", &matches).Error
	if err != nil {
		return false, fmt.Errorf("gormrepo: check directory: %w", err)
	}
	return len(matches) > 0, nil
}

func (r *Repo) ListTreeEntries(ctx context.Context, workspaceID, prefix, cursor string, limit int) ([]*workspace.Entry, bool, error) {
	conn := r.conn(ctx)
	rows := conn.Raw(`
		SELECT path, revision_id, updated_at FROM workspace_entries WHERE workspace_id = ? AND deleted = ?
		UNION ALL
		SELECT path, '' AS revision_id, created_at AS updated_at FROM workspace_directories WHERE workspace_id = ?`,
		workspaceID, false, workspaceID)
	query := conn.Table("(?) AS tree_entries", rows)
	if prefix != "" {
		lo, hi := prefixRange(prefix)
		query = query.Where("path >= ?", lo)
		if hi != "" {
			query = query.Where("path < ?", hi)
		}
	}
	if cursor != "" {
		query = query.Where("path > ?", cursor)
	}
	var entries []*workspace.Entry
	if err := query.Order("path ASC").Limit(limit + 1).Scan(&entries).Error; err != nil {
		return nil, false, fmt.Errorf("gormrepo: list directory tree: %w", err)
	}
	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}
	return entries, hasMore, nil
}
