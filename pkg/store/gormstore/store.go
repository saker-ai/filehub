package gormstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/saker-ai/assethub/pkg/store"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type Store struct {
	db *gorm.DB
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := openDB(dsn)
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("database handle: %w", err)
	}
	if strings.HasPrefix(dsn, "sqlite://") {
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	} else {
		sqlDB.SetMaxOpenConns(25)
		sqlDB.SetMaxIdleConns(10)
	}
	sqlDB.SetConnMaxLifetime(5 * time.Minute)
	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("database ping: %w", err)
	}
	if err := db.WithContext(ctx).AutoMigrate(&AssetModel{}, &TagModel{}, &AssetTagModel{}, &UploadSessionModel{}, &UploadPartModel{}); err != nil {
		return nil, fmt.Errorf("database migrate: %w", err)
	}
	return &Store{db: db}, nil
}

func openDB(dsn string) (*gorm.DB, error) {
	if strings.HasPrefix(dsn, "sqlite://") {
		path := strings.TrimPrefix(dsn, "sqlite://")
		if err := ensureSQLiteDir(path); err != nil {
			return nil, err
		}
		if !strings.Contains(path, "?") {
			path += "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
		}
		return gorm.Open(sqlite.Open(path), &gorm.Config{})
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return gorm.Open(postgres.Open(dsn), &gorm.Config{})
	}
	return nil, fmt.Errorf("unsupported database dsn %q", dsn)
}

func ensureSQLiteDir(path string) error {
	dbPath, _, _ := strings.Cut(path, "?")
	if dbPath == "" || dbPath == ":memory:" || strings.HasPrefix(dbPath, "file:") {
		return nil
	}
	dir := filepath.Dir(dbPath)
	if dir == "." || dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create sqlite database dir: %w", err)
	}
	return nil
}

func isUniqueConstraintError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "duplicate entry")
}

func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func (s *Store) Create(ctx context.Context, a *store.Asset) error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	a.UpdatedAt = a.CreatedAt
	if a.Metadata == nil {
		a.Metadata = store.JSONMap{}
	}
	m := fromAsset(a)
	if err := s.db.WithContext(ctx).Create(&m).Error; err != nil {
		if isUniqueConstraintError(err) {
			return store.ErrConflict
		}
		return fmt.Errorf("create asset: %w", err)
	}
	if len(a.Tags) > 0 {
		names := make([]string, 0, len(a.Tags))
		for _, tag := range a.Tags {
			names = append(names, tag.Name)
		}
		if err := s.SetTags(ctx, a.ID, names); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Get(ctx context.Context, tenantID, id string) (*store.Asset, error) {
	var m AssetModel
	err := s.db.WithContext(ctx).Preload("Tags").Where("tenant_id = ? AND id = ?", tenantID, id).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get asset: %w", err)
	}
	return toAsset(m), nil
}

func (s *Store) FindByChecksum(ctx context.Context, tenantID, checksum string) (*store.Asset, error) {
	var m AssetModel
	err := s.db.WithContext(ctx).Preload("Tags").Where("tenant_id = ? AND checksum = ?", tenantID, checksum).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find checksum: %w", err)
	}
	return toAsset(m), nil
}

func (s *Store) List(ctx context.Context, tenantID string, filter store.AssetFilter) ([]*store.Asset, bool, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	q := s.db.WithContext(ctx).Model(&AssetModel{}).Preload("Tags").Where("tenant_id = ?", tenantID)
	q = applyAssetFilter(q, filter)
	if len(filter.Tags) > 0 {
		q = q.Where(`id IN (
			SELECT asset_id FROM asset_tags
			JOIN tags ON tags.id = asset_tags.tag_id
			WHERE tags.name IN ?
			GROUP BY asset_id HAVING COUNT(DISTINCT tags.name) = ?
		)`, filter.Tags, len(filter.Tags))
	}
	order := "created_at DESC"
	if strings.EqualFold(filter.Order, "asc") {
		order = "created_at ASC"
	}
	var models []AssetModel
	if err := q.Order(order).Limit(limit + 1).Offset(filter.Offset).Find(&models).Error; err != nil {
		return nil, false, fmt.Errorf("list assets: %w", err)
	}
	hasMore := len(models) > limit
	if hasMore {
		models = models[:limit]
	}
	out := make([]*store.Asset, 0, len(models))
	for _, m := range models {
		out = append(out, toAsset(m))
	}
	return out, hasMore, nil
}

func applyAssetFilter(q *gorm.DB, f store.AssetFilter) *gorm.DB {
	if f.Purpose != "" {
		q = q.Where("purpose = ?", f.Purpose)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.Filename != "" {
		q = q.Where("filename LIKE ?", "%"+f.Filename+"%")
	}
	if f.Source != "" {
		q = q.Where("source = ?", f.Source)
	}
	if f.ContentType != "" {
		if strings.HasSuffix(f.ContentType, "/") {
			q = q.Where("content_type LIKE ?", f.ContentType+"%")
		} else {
			q = q.Where("content_type = ?", f.ContentType)
		}
	}
	if f.IDPrefix != "" {
		q = q.Where("id LIKE ?", f.IDPrefix+"%")
	}
	if f.MetaModel != "" {
		q = applyMetadataFilter(q, store.MetadataFilter{Key: "model", Value: f.MetaModel, HasValue: true})
	}
	if f.MetaQuery != "" {
		q = q.Where("metadata LIKE ?", "%"+f.MetaQuery+"%")
	}
	for _, mf := range f.Metadata {
		q = applyMetadataFilter(q, mf)
	}
	if f.After != "" {
		q = q.Where("id > ?", f.After)
	}
	if f.Before != "" {
		q = q.Where("id < ?", f.Before)
	}
	return q
}

func applyMetadataFilter(q *gorm.DB, f store.MetadataFilter) *gorm.DB {
	key := strings.TrimSpace(f.Key)
	if key == "" {
		return q
	}
	keyJSON, err := json.Marshal(key)
	if err != nil {
		return q
	}
	prefix := escapeLike(string(keyJSON) + ":")
	if !f.HasValue {
		return q.Where(`metadata LIKE ? ESCAPE '\'`, "%"+prefix+"%")
	}
	valueJSON, err := json.Marshal(f.Value)
	if err != nil {
		return q
	}
	return q.Where(`metadata LIKE ? ESCAPE '\'`, "%"+prefix+escapeLike(string(valueJSON))+"%")
}

func escapeLike(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\', '%', '_':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (s *Store) Update(ctx context.Context, a *store.Asset) error {
	a.UpdatedAt = time.Now().UTC()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&AssetModel{}).Where("tenant_id = ? AND id = ?", a.TenantID, a.ID).Updates(map[string]any{
			"purpose":      a.Purpose,
			"filename":     a.Filename,
			"content_type": a.ContentType,
			"bytes":        a.Bytes,
			"storage_key":  a.StorageKey,
			"checksum":     a.Checksum,
			"dedupe_key":   a.DedupeKey,
			"status":       a.Status,
			"source":       a.Source,
			"metadata":     a.Metadata,
			"updated_at":   a.UpdatedAt,
			"expires_at":   a.ExpiresAt,
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return store.ErrNotFound
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("update asset: %w", err)
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, tenantID, id string) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("asset_id = ?", id).Delete(&AssetTagModel{}).Error; err != nil {
			return err
		}
		res := tx.Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&AssetModel{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return store.ErrNotFound
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete asset: %w", err)
	}
	return nil
}

func (s *Store) UpdateStatus(ctx context.Context, id, status string) error {
	res := s.db.WithContext(ctx).Model(&AssetModel{}).Where("id = ?", id).Updates(map[string]any{
		"status":     status,
		"updated_at": time.Now().UTC(),
	})
	if res.Error != nil {
		return fmt.Errorf("update asset status: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) SetTags(ctx context.Context, id string, tags []string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var asset AssetModel
		if err := tx.Where("id = ?", id).First(&asset).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return store.ErrNotFound
			}
			return err
		}
		if err := tx.Where("asset_id = ?", id).Delete(&AssetTagModel{}).Error; err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, raw := range tags {
			name := strings.TrimSpace(raw)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			t := TagModel{Name: name}
			if err := tx.Where("name = ?", name).FirstOrCreate(&t, TagModel{Name: name}).Error; err != nil {
				return err
			}
			if err := tx.Create(&AssetTagModel{AssetID: asset.ID, TagID: t.ID}).Error; err != nil {
				return err
			}
		}
		return tx.Model(&AssetModel{}).Where("id = ?", id).Update("updated_at", time.Now().UTC()).Error
	})
}

func (s *Store) ListExpired(ctx context.Context, now time.Time, limit int) ([]*store.Asset, error) {
	if limit <= 0 {
		limit = 100
	}
	var models []AssetModel
	if err := s.db.WithContext(ctx).Preload("Tags").Where("expires_at IS NOT NULL AND expires_at <= ?", now).Limit(limit).Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list expired assets: %w", err)
	}
	out := make([]*store.Asset, 0, len(models))
	for _, m := range models {
		out = append(out, toAsset(m))
	}
	return out, nil
}

func (s *Store) Stats(ctx context.Context, tenantID string) (*store.AssetStats, error) {
	stats := &store.AssetStats{
		ByPurpose:     map[string]int64{},
		ByContentType: map[string]int64{},
		BySource:      map[string]int64{},
		ByStatus:      map[string]int64{},
	}
	base := func() *gorm.DB {
		return s.db.WithContext(ctx).Model(&AssetModel{}).Where("tenant_id = ?", tenantID)
	}
	if err := base().Count(&stats.Total).Error; err != nil {
		return nil, fmt.Errorf("count assets: %w", err)
	}
	if err := base().Select("COALESCE(SUM(bytes), 0)").Scan(&stats.TotalBytes).Error; err != nil {
		return nil, fmt.Errorf("sum assets: %w", err)
	}
	if err := groupCounts(base(), "purpose", stats.ByPurpose); err != nil {
		return nil, err
	}
	if err := groupCounts(base(), "source", stats.BySource); err != nil {
		return nil, err
	}
	if err := groupCounts(base(), "status", stats.ByStatus); err != nil {
		return nil, err
	}
	var rows []struct {
		ContentType string
		Count       int64
	}
	if err := base().Select("content_type, COUNT(*) as count").Group("content_type").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("group content type: %w", err)
	}
	for _, row := range rows {
		stats.ByContentType[contentFamily(row.ContentType)] += row.Count
	}
	return stats, nil
}

func groupCounts(q *gorm.DB, column string, dst map[string]int64) error {
	var rows []struct {
		Key   string
		Count int64
	}
	if err := q.Select(column + " as key, COUNT(*) as count").Group(column).Scan(&rows).Error; err != nil {
		return fmt.Errorf("group %s: %w", column, err)
	}
	for _, row := range rows {
		if row.Key == "" {
			row.Key = "unknown"
		}
		dst[row.Key] = row.Count
	}
	return nil
}

func contentFamily(ct string) string {
	switch {
	case strings.HasPrefix(ct, "image/"):
		return "image"
	case strings.HasPrefix(ct, "video/"):
		return "video"
	case strings.HasPrefix(ct, "audio/"):
		return "audio"
	case strings.HasPrefix(ct, "text/"), ct == "application/json", ct == "application/xml", ct == "application/pdf":
		return "text"
	case strings.HasPrefix(ct, "model/"):
		return "model"
	default:
		return "other"
	}
}

func (s *Store) CreateSession(ctx context.Context, sess *store.UploadSession) error {
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now().UTC()
	}
	if sess.ChunkSize <= 0 {
		sess.ChunkSize = 10 * 1024 * 1024
	}
	if sess.Status == "" {
		sess.Status = "pending"
	}
	if sess.Metadata == nil {
		sess.Metadata = store.JSONMap{}
	}
	m := fromSession(sess)
	if err := s.db.WithContext(ctx).Create(&m).Error; err != nil {
		return fmt.Errorf("create upload session: %w", err)
	}
	return nil
}

func (s *Store) GetSession(ctx context.Context, tenantID, id string) (*store.UploadSession, error) {
	var m UploadSessionModel
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get upload session: %w", err)
	}
	return toSession(m), nil
}

func (s *Store) AddPart(ctx context.Context, part *store.UploadPart) error {
	m := UploadPartModel{UploadID: part.UploadID, PartNum: part.PartNum, ETag: part.ETag, Bytes: part.Bytes}
	if err := s.db.WithContext(ctx).Save(&m).Error; err != nil {
		return fmt.Errorf("save upload part: %w", err)
	}
	return nil
}

func (s *Store) ListParts(ctx context.Context, uploadID string) ([]*store.UploadPart, error) {
	var models []UploadPartModel
	if err := s.db.WithContext(ctx).Where("upload_id = ?", uploadID).Order("part_num ASC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list upload parts: %w", err)
	}
	out := make([]*store.UploadPart, 0, len(models))
	for _, m := range models {
		out = append(out, &store.UploadPart{UploadID: m.UploadID, PartNum: m.PartNum, ETag: m.ETag, Bytes: m.Bytes})
	}
	return out, nil
}

func (s *Store) UpdateSessionStatus(ctx context.Context, id, status string) error {
	res := s.db.WithContext(ctx).Model(&UploadSessionModel{}).Where("id = ?", id).Update("status", status)
	if res.Error != nil {
		return fmt.Errorf("update upload status: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("upload_id = ?", id).Delete(&UploadPartModel{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&UploadSessionModel{}).Error
	})
}

func (s *Store) ListExpiredSessions(ctx context.Context, now time.Time, limit int) ([]*store.UploadSession, error) {
	if limit <= 0 {
		limit = 100
	}
	var models []UploadSessionModel
	if err := s.db.WithContext(ctx).Where("expires_at <= ? AND status != ?", now, "completed").Limit(limit).Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list expired sessions: %w", err)
	}
	out := make([]*store.UploadSession, 0, len(models))
	for _, m := range models {
		out = append(out, toSession(m))
	}
	return out, nil
}
