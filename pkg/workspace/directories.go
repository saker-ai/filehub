package workspace

import (
	"context"
	"errors"
	"fmt"
	"path"
	"time"
)

// Directory is an explicitly saved path. File paths can also imply ancestors.
type Directory struct {
	WorkspaceID string
	Path        string
	CreatedAt   time.Time
}

// DirectoryRepo persists explicit directory paths and supports a paginated
// listing that includes both files and directories.
type DirectoryRepo interface {
	SaveDirectory(context.Context, *Directory) error
	DeleteDirectory(ctx context.Context, workspaceID, directoryPath string) error
	DirectoryExists(ctx context.Context, workspaceID, pathOrAncestor string) (bool, error)
	ListTreeEntries(ctx context.Context, workspaceID, prefix, cursor string, limit int) ([]*Entry, bool, error)
}

// CreateDirectory persists an empty directory. Repeating the same operation
// leaves the original directory and timestamp intact.
func (s *Service) CreateDirectory(ctx context.Context, tenantID, workspaceID, p string) error {
	return s.editDirectory(ctx, tenantID, workspaceID, p, func(ctx context.Context) error {
		for ancestor := p; ancestor != "."; ancestor = path.Dir(ancestor) {
			entry, err := s.currentEntry(ctx, workspaceID, ancestor)
			if err != nil {
				return err
			}
			if entry != nil {
				return ErrPathConflict
			}
		}
		return s.directories.SaveDirectory(ctx, &Directory{WorkspaceID: workspaceID, Path: p, CreatedAt: time.Now().UTC()})
	})
}

// DeleteDirectory removes the explicit path when it has no descendants.
// A repeated deletion succeeds as long as the path has no descendants.
func (s *Service) DeleteDirectory(ctx context.Context, tenantID, workspaceID, p string) error {
	return s.editDirectory(ctx, tenantID, workspaceID, p, func(ctx context.Context) error {
		children, _, err := s.directories.ListTreeEntries(ctx, workspaceID, p+"/", "", 1)
		if err != nil {
			return err
		}
		if len(children) != 0 {
			return ErrDirectoryNotEmpty
		}
		return s.directories.DeleteDirectory(ctx, workspaceID, p)
	})
}

func (s *Service) editDirectory(ctx context.Context, tenantID, workspaceID, p string, edit func(context.Context) error) error {
	if s.directories == nil {
		return ErrDirectoriesUnavailable
	}
	if err := ValidatePath(p, s.limits); err != nil {
		return err
	}
	if IsExcluded(p) {
		return fmt.Errorf("%w: %s", ErrExcludedPath, ExcludedReason(p))
	}
	return s.repo.WithTx(ctx, func(ctx context.Context) error {
		ws, err := s.repo.LockWorkspace(ctx, tenantID, workspaceID)
		if err != nil {
			return err
		}
		if ws.DeletedAt != nil {
			return ErrGone
		}
		return edit(ctx)
	})
}

// checkFileDestination keeps file commits and restores from occupying an
// explicitly registered directory (including the ancestors it implies).
func (s *Service) checkFileDestination(ctx context.Context, workspaceID, p string) error {
	if s.directories == nil {
		return nil
	}
	exists, err := s.directories.DirectoryExists(ctx, workspaceID, p)
	if err != nil {
		return err
	}
	if exists {
		return ErrPathConflict
	}
	return nil
}

var (
	// ErrDirectoriesUnavailable reports a repository without explicit directory storage.
	ErrDirectoriesUnavailable = errors.New("workspace: explicit directories unavailable")
	// ErrPathConflict reports a file that occupies a directory path or vice versa.
	ErrPathConflict = errors.New("workspace: a file and directory cannot occupy the same path")
	// ErrDirectoryNotEmpty reports a directory that still has live descendants.
	ErrDirectoryNotEmpty = errors.New("workspace: directory is not empty")
)
