package gormrepo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
)

// TreeScope identifies a logical file tree using the host's authenticated
// tenant, owner and collection ID. It is separate from desktop sync IDs.
type TreeScope struct {
	TenantID string
	OwnerID  string
	TreeID   string
}

func (s TreeScope) key() (string, error) {
	if s.TenantID == "" || s.OwnerID == "" || s.TreeID == "" {
		return "", fmt.Errorf("managed tree: incomplete scope")
	}
	h := sha256.New()
	for _, part := range [...]string{s.TenantID, s.OwnerID, s.TreeID} {
		fmt.Fprintf(h, "%d:%s", len(part), part)
	}
	return "managed-" + hex.EncodeToString(h.Sum(nil)), nil
}

// ManagedFile places a host-authorized, immutable file binding in a folder.
// Multiple bindings can share a folder or even the same display name.
type ManagedFile struct {
	Position int
	Folder   string
	ID       string
	Version  string
}

type ManagedTree struct {
	Folders []string
	Files   []ManagedFile
}

type managedFileModel struct {
	TreeID   string `gorm:"type:text;primaryKey"`
	Position int    `gorm:"primaryKey;autoIncrement:false"`
	Folder   string `gorm:"type:text;not null"`
	FileID   string `gorm:"type:text;not null"`
	Version  string `gorm:"type:text;not null"`
}

func (managedFileModel) TableName() string { return "workspace_managed_files" }

// MigrateManagedTrees installs storage for embedded logical trees. The host
// can run it in the same migration transaction as its own metadata.
func MigrateManagedTrees(ctx context.Context, db *gorm.DB) error {
	if err := db.WithContext(ctx).AutoMigrate(&DirectoryModel{}, &managedFileModel{}); err != nil {
		return fmt.Errorf("migrate managed trees: %w", err)
	}
	return nil
}

// LoadManagedTrees reads folder placements and pinned references. Pass the
// host's read transaction to get a consistent view of both systems.
func LoadManagedTrees(ctx context.Context, db *gorm.DB, scopes []TreeScope) (map[TreeScope]ManagedTree, error) {
	out := make(map[TreeScope]ManagedTree, len(scopes))
	db = db.WithContext(ctx)
	for batch := range slices.Chunk(scopes, 200) {
		keys := make([]string, 0, len(batch))
		byKey := make(map[string]TreeScope, len(batch))
		for _, scope := range batch {
			key, err := scope.key()
			if err != nil {
				return nil, err
			}
			keys = append(keys, key)
			byKey[key] = scope
		}
		var folders []DirectoryModel
		if err := db.Where("workspace_id IN ?", keys).Find(&folders).Error; err != nil {
			return nil, fmt.Errorf("load managed folders: %w", err)
		}
		for _, row := range folders {
			scope := byKey[row.WorkspaceID]
			tree := out[scope]
			tree.Folders = append(tree.Folders, row.Path)
			out[scope] = tree
		}
		var files []managedFileModel
		if err := db.Where("tree_id IN ?", keys).Order("position ASC").Find(&files).Error; err != nil {
			return nil, fmt.Errorf("load managed files: %w", err)
		}
		for _, row := range files {
			scope := byKey[row.TreeID]
			tree := out[scope]
			tree.Files = append(tree.Files, ManagedFile{Position: row.Position, Folder: row.Folder, ID: row.FileID, Version: row.Version})
			out[scope] = tree
		}
	}
	for _, tree := range out {
		slices.Sort(tree.Folders)
	}
	return out, nil
}

// ReplaceManagedTree writes a complete tree using the supplied host transaction.
// The host must authorize the scope and file bindings and serialize updates to
// the tree. Supplying an empty tree removes placements without deleting files.
func ReplaceManagedTree(ctx context.Context, tx *gorm.DB, scope TreeScope, tree ManagedTree) error {
	key, err := scope.key()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	folders := make([]DirectoryModel, 0, len(tree.Folders))
	for _, folder := range tree.Folders {
		if !validManagedFolder(folder) {
			return fmt.Errorf("managed tree: invalid folder path")
		}
		folders = append(folders, DirectoryModel{WorkspaceID: key, Path: folder, CreatedAt: now})
	}
	files := make([]managedFileModel, 0, len(tree.Files))
	for _, file := range tree.Files {
		if file.Position < 0 || file.ID == "" || file.Version == "" || file.Folder != "" && !validManagedFolder(file.Folder) {
			return fmt.Errorf("managed tree: invalid file placement")
		}
		files = append(files, managedFileModel{TreeID: key, Position: file.Position, Folder: file.Folder, FileID: file.ID, Version: file.Version})
	}
	tx = tx.WithContext(ctx)
	if err := tx.Where("workspace_id = ?", key).Delete(&DirectoryModel{}).Error; err != nil {
		return fmt.Errorf("clear managed folders: %w", err)
	}
	if err := tx.Where("tree_id = ?", key).Delete(&managedFileModel{}).Error; err != nil {
		return fmt.Errorf("clear managed files: %w", err)
	}
	if len(folders) > 0 {
		if err := tx.CreateInBatches(folders, 100).Error; err != nil {
			return fmt.Errorf("save managed folders: %w", err)
		}
	}
	if len(files) > 0 {
		if err := tx.CreateInBatches(files, 100).Error; err != nil {
			return fmt.Errorf("save managed files: %w", err)
		}
	}
	return nil
}

// Logical placements can use labels that desktop sync clients reserve.
func validManagedFolder(path string) bool {
	if strings.ContainsRune(path, '\\') || strings.ContainsRune(path, 0) {
		return false
	}
	for segment := range strings.SplitSeq(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}
