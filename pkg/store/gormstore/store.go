package gormstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/saker-ai/filehub/pkg/store"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type Store struct {
	db *gorm.DB
}

type connectionPoolConfig struct {
	maxOpenConns    int
	maxIdleConns    int
	connMaxLifetime time.Duration
	connMaxIdleTime time.Duration
}

func connectionPoolConfigForDSN(dsn string) connectionPoolConfig {
	if strings.HasPrefix(dsn, "sqlite://") {
		// A SQLite in-memory database belongs to its connection. Keep the single
		// connection alive so pool rotation cannot replace the migrated schema
		// with a fresh, empty database. The same policy avoids needless churn and
		// locking for file-backed SQLite.
		return connectionPoolConfig{
			maxOpenConns: 1,
			maxIdleConns: 1,
		}
	}
	return connectionPoolConfig{
		maxOpenConns:    25,
		maxIdleConns:    10,
		connMaxLifetime: 5 * time.Minute,
	}
}

func (c connectionPoolConfig) apply(db *sql.DB) {
	db.SetMaxOpenConns(c.maxOpenConns)
	db.SetMaxIdleConns(c.maxIdleConns)
	db.SetConnMaxLifetime(c.connMaxLifetime)
	db.SetConnMaxIdleTime(c.connMaxIdleTime)
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
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = sqlDB.Close()
		}
	}()
	connectionPoolConfigForDSN(dsn).apply(sqlDB)
	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("database ping: %w", err)
	}
	if err := db.WithContext(ctx).AutoMigrate(&AssetModel{}, &TagModel{}, &AssetTagModel{}, &AssetMetadataModel{}, &AIReviewModel{}, &AssetReviewModel{}, &AssetReviewItemModel{}, &UploadSessionModel{}, &UploadPartModel{}); err != nil {
		return nil, fmt.Errorf("database migrate: %w", err)
	}
	out := &Store{db: db}
	if err := out.ensureMetadataIndex(ctx); err != nil {
		return nil, err
	}
	closeOnError = false
	return out, nil
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
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&m).Error; err != nil {
			if isUniqueConstraintError(err) {
				return store.ErrConflict
			}
			return fmt.Errorf("create asset: %w", err)
		}
		return replaceMetadataIndex(tx, a.ID, a.Metadata)
	}); err != nil {
		return err
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
		order = "created_at ASC, id ASC"
	} else {
		order = "created_at DESC, id DESC"
	}
	q = applyCursor(q, filter)
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
	sub := q.Session(&gorm.Session{NewDB: true}).Model(&AssetMetadataModel{}).Select("asset_id")
	if !f.HasValue {
		return q.Where("id IN (?)", sub.Where("key = ?", key))
	}
	valueType, valueText, ok := metadataValueParts(f.Value)
	if !ok {
		return q
	}
	return q.Where("id IN (?)", sub.Where("key = ? AND value_type = ? AND value_text = ?", key, valueType, valueText))
}

func applyCursor(q *gorm.DB, f store.AssetFilter) *gorm.DB {
	cursor := strings.TrimSpace(f.Cursor)
	if cursor == "" {
		return q
	}
	createdAt, id, ok := decodeCursor(cursor)
	if !ok {
		return q
	}
	if strings.EqualFold(f.Order, "asc") {
		return q.Where("(created_at > ? OR (created_at = ? AND id > ?))", createdAt, createdAt, id)
	}
	return q.Where("(created_at < ? OR (created_at = ? AND id < ?))", createdAt, createdAt, id)
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
		return replaceMetadataIndex(tx, a.ID, a.Metadata)
	})
	if err != nil {
		return fmt.Errorf("update asset: %w", err)
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, tenantID, id string) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id = ? AND asset_id = ?", tenantID, id).Delete(&AIReviewModel{}).Error; err != nil {
			return err
		}
		if err := tx.Where("asset_id = ? AND review_id IN (SELECT id FROM asset_reviews WHERE tenant_id = ?)", id, tenantID).Delete(&AssetReviewItemModel{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&AssetReviewModel{}).Where("tenant_id = ? AND selected_asset_id = ?", tenantID, id).Update("selected_asset_id", "").Error; err != nil {
			return err
		}
		if err := tx.Where("asset_id = ?", id).Delete(&AssetTagModel{}).Error; err != nil {
			return err
		}
		if err := tx.Where("asset_id = ?", id).Delete(&AssetMetadataModel{}).Error; err != nil {
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

func (s *Store) CreateAIReview(ctx context.Context, r *store.AIReview) error {
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	r.UpdatedAt = r.CreatedAt
	if r.Metadata == nil {
		r.Metadata = store.JSONMap{}
	}
	m := fromAIReview(r)
	if err := s.db.WithContext(ctx).Create(&m).Error; err != nil {
		if isUniqueConstraintError(err) {
			return store.ErrConflict
		}
		return fmt.Errorf("create ai review: %w", err)
	}
	return nil
}

func (s *Store) GetAIReview(ctx context.Context, tenantID, id string) (*store.AIReview, error) {
	var m AIReviewModel
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get ai review: %w", err)
	}
	return toAIReview(m), nil
}

func (s *Store) ListAIReviews(ctx context.Context, tenantID string, filter store.AIReviewFilter) ([]*store.AIReview, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	q := s.db.WithContext(ctx).Model(&AIReviewModel{}).Where("tenant_id = ?", tenantID)
	if filter.AssetID != "" {
		q = q.Where("asset_id = ?", filter.AssetID)
	}
	if filter.Verdict != "" {
		q = q.Where("verdict = ?", filter.Verdict)
	}
	if filter.Model != "" {
		q = q.Where("model = ?", filter.Model)
	}
	var models []AIReviewModel
	if err := q.Order("created_at DESC, id DESC").Limit(limit).Offset(filter.Offset).Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list ai reviews: %w", err)
	}
	out := make([]*store.AIReview, 0, len(models))
	for _, m := range models {
		out = append(out, toAIReview(m))
	}
	return out, nil
}

func (s *Store) DeleteAIReviewsForAsset(ctx context.Context, tenantID, assetID string) error {
	if err := s.db.WithContext(ctx).Where("tenant_id = ? AND asset_id = ?", tenantID, assetID).Delete(&AIReviewModel{}).Error; err != nil {
		return fmt.Errorf("delete ai reviews for asset: %w", err)
	}
	return nil
}

func (s *Store) CreateAssetReview(ctx context.Context, r *store.AssetReview) error {
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	r.UpdatedAt = r.CreatedAt
	if r.Status == "" {
		r.Status = "open"
	}
	if r.Source == "" {
		r.Source = "human"
	}
	if r.Metadata == nil {
		r.Metadata = store.JSONMap{}
	}
	for i := range r.Items {
		if r.Items[i].CreatedAt.IsZero() {
			r.Items[i].CreatedAt = r.CreatedAt
		}
		r.Items[i].UpdatedAt = r.Items[i].CreatedAt
		r.Items[i].ReviewID = r.ID
		if r.Items[i].Metadata == nil {
			r.Items[i].Metadata = store.JSONMap{}
		}
	}
	m := fromAssetReview(r)
	if err := s.db.WithContext(ctx).Create(&m).Error; err != nil {
		if isUniqueConstraintError(err) {
			return store.ErrConflict
		}
		return fmt.Errorf("create asset review: %w", err)
	}
	return nil
}

func (s *Store) GetAssetReview(ctx context.Context, tenantID, id string) (*store.AssetReview, error) {
	var m AssetReviewModel
	err := s.db.WithContext(ctx).Preload("Items", func(db *gorm.DB) *gorm.DB {
		return db.Order("created_at ASC, id ASC")
	}).Where("tenant_id = ? AND id = ?", tenantID, id).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get asset review: %w", err)
	}
	return toAssetReview(m), nil
}

func (s *Store) ListAssetReviews(ctx context.Context, tenantID string, filter store.AssetReviewFilter) ([]*store.AssetReview, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	q := s.db.WithContext(ctx).Model(&AssetReviewModel{}).Preload("Items").Where("tenant_id = ?", tenantID)
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if filter.Reviewer != "" {
		q = q.Where("reviewer = ?", filter.Reviewer)
	}
	if filter.Source != "" {
		q = q.Where("source = ?", filter.Source)
	}
	var models []AssetReviewModel
	if err := q.Order("created_at DESC, id DESC").Limit(limit).Offset(filter.Offset).Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list asset reviews: %w", err)
	}
	out := make([]*store.AssetReview, 0, len(models))
	for _, m := range models {
		out = append(out, toAssetReview(m))
	}
	return out, nil
}

func (s *Store) UpdateAssetReview(ctx context.Context, r *store.AssetReview) error {
	r.UpdatedAt = time.Now().UTC()
	updates := map[string]any{
		"title":             r.Title,
		"status":            r.Status,
		"reference_id":      r.ReferenceID,
		"selected_asset_id": r.SelectedAssetID,
		"reviewer":          r.Reviewer,
		"source":            r.Source,
		"trace_id":          r.TraceID,
		"metadata":          r.Metadata,
		"updated_at":        r.UpdatedAt,
		"completed_at":      r.CompletedAt,
	}
	res := s.db.WithContext(ctx).Model(&AssetReviewModel{}).Where("tenant_id = ? AND id = ?", r.TenantID, r.ID).Updates(updates)
	if res.Error != nil {
		return fmt.Errorf("update asset review: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) UpdateAssetReviewItem(ctx context.Context, tenantID, reviewID string, item *store.AssetReviewItem) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var review AssetReviewModel
		if err := tx.Select("id").Where("tenant_id = ? AND id = ?", tenantID, reviewID).First(&review).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return store.ErrNotFound
			}
			return fmt.Errorf("get asset review for item update: %w", err)
		}
		now := time.Now().UTC()
		var existing AssetReviewItemModel
		err := tx.Where("review_id = ? AND asset_id = ?", reviewID, item.AssetID).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if item.ID == "" {
				return store.ErrNotFound
			}
			item.ReviewID = reviewID
			item.CreatedAt = now
			item.UpdatedAt = now
			if item.Metadata == nil {
				item.Metadata = store.JSONMap{}
			}
			m := fromAssetReviewItem(item)
			if err := tx.Create(&m).Error; err != nil {
				return fmt.Errorf("create asset review item: %w", err)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("get asset review item: %w", err)
		}
		if item.Metadata == nil {
			item.Metadata = store.JSONMap{}
		}
		updates := map[string]any{
			"decision":   item.Decision,
			"note":       item.Note,
			"score":      item.Score,
			"metadata":   item.Metadata,
			"updated_at": now,
		}
		if err := tx.Model(&AssetReviewItemModel{}).Where("id = ?", existing.ID).Updates(updates).Error; err != nil {
			return fmt.Errorf("update asset review item: %w", err)
		}
		return nil
	})
}

func (s *Store) DeleteAssetReviewsForAsset(ctx context.Context, tenantID, assetID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("asset_id = ? AND review_id IN (SELECT id FROM asset_reviews WHERE tenant_id = ?)", assetID, tenantID).Delete(&AssetReviewItemModel{}).Error; err != nil {
			return fmt.Errorf("delete asset review items for asset: %w", err)
		}
		if err := tx.Model(&AssetReviewModel{}).Where("tenant_id = ? AND selected_asset_id = ?", tenantID, assetID).Update("selected_asset_id", "").Error; err != nil {
			return fmt.Errorf("clear selected asset review references: %w", err)
		}
		return nil
	})
}

func (s *Store) ensureMetadataIndex(ctx context.Context) error {
	var assets int64
	if err := s.db.WithContext(ctx).Model(&AssetModel{}).Count(&assets).Error; err != nil {
		return fmt.Errorf("count assets for metadata index: %w", err)
	}
	if assets == 0 {
		return nil
	}
	var indexed int64
	if err := s.db.WithContext(ctx).Model(&AssetMetadataModel{}).Count(&indexed).Error; err != nil {
		return fmt.Errorf("count metadata index: %w", err)
	}
	if indexed > 0 {
		return nil
	}
	var models []AssetModel
	if err := s.db.WithContext(ctx).Find(&models).Error; err != nil {
		return fmt.Errorf("load assets for metadata index: %w", err)
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, model := range models {
			if err := replaceMetadataIndex(tx, model.ID, model.Metadata); err != nil {
				return err
			}
		}
		return nil
	})
}

func replaceMetadataIndex(tx *gorm.DB, assetID string, metadata store.JSONMap) error {
	if err := tx.Where("asset_id = ?", assetID).Delete(&AssetMetadataModel{}).Error; err != nil {
		return err
	}
	rows := metadataIndexRows(assetID, metadata)
	if len(rows) == 0 {
		return nil
	}
	return tx.Create(&rows).Error
}

func metadataIndexRows(assetID string, metadata store.JSONMap) []AssetMetadataModel {
	rows := make([]AssetMetadataModel, 0, len(metadata))
	for key, value := range metadata {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		valueType, valueText, ok := metadataValueParts(value)
		if !ok {
			continue
		}
		rows = append(rows, AssetMetadataModel{AssetID: assetID, Key: key, ValueType: valueType, ValueText: valueText})
	}
	return rows
}

func metadataValueParts(value any) (string, string, bool) {
	switch v := value.(type) {
	case nil:
		return "null", "null", true
	case bool:
		if v {
			return "bool", "true", true
		}
		return "bool", "false", true
	case string:
		return "string", v, true
	case json.Number:
		return "number", v.String(), true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return "", "", false
		}
		return "number", strconv.FormatFloat(v, 'f', -1, 64), true
	case float32:
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return "", "", false
		}
		return "number", strconv.FormatFloat(f, 'f', -1, 32), true
	case int:
		return "number", strconv.FormatInt(int64(v), 10), true
	case int8:
		return "number", strconv.FormatInt(int64(v), 10), true
	case int16:
		return "number", strconv.FormatInt(int64(v), 10), true
	case int32:
		return "number", strconv.FormatInt(int64(v), 10), true
	case int64:
		return "number", strconv.FormatInt(v, 10), true
	case uint:
		return "number", strconv.FormatUint(uint64(v), 10), true
	case uint8:
		return "number", strconv.FormatUint(uint64(v), 10), true
	case uint16:
		return "number", strconv.FormatUint(uint64(v), 10), true
	case uint32:
		return "number", strconv.FormatUint(uint64(v), 10), true
	case uint64:
		return "number", strconv.FormatUint(v, 10), true
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return "", "", false
		}
		return "json", string(data), true
	}
}

func DecodeCursorForTest(cursor string) (time.Time, string, bool) {
	return decodeCursor(cursor)
}

func decodeCursor(cursor string) (time.Time, string, bool) {
	rawTime, id, ok := strings.Cut(cursor, ":")
	if !ok || rawTime == "" || id == "" {
		return time.Time{}, "", false
	}
	nanos, err := strconv.ParseInt(rawTime, 10, 64)
	if err != nil {
		return time.Time{}, "", false
	}
	return time.Unix(0, nanos).UTC(), id, true
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

func (s *Store) TransitionStatus(ctx context.Context, id, from, to string) (bool, error) {
	res := s.db.WithContext(ctx).Model(&AssetModel{}).
		Where("id = ? AND status = ?", id, from).
		Updates(map[string]any{"status": to, "updated_at": time.Now().UTC()})
	if res.Error != nil {
		return false, fmt.Errorf("transition asset status: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
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
	if sess.Mode == "" {
		sess.Mode = "proxy"
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

func (s *Store) TransitionSessionStatus(ctx context.Context, id, from, to string) (bool, error) {
	res := s.db.WithContext(ctx).Model(&UploadSessionModel{}).
		Where("id = ? AND status = ?", id, from).
		Update("status", to)
	if res.Error != nil {
		return false, fmt.Errorf("transition upload status: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

func (s *Store) ClaimSessionCompletion(ctx context.Context, id string, leaseUntil time.Time) (bool, error) {
	res := s.db.WithContext(ctx).Model(&UploadSessionModel{}).
		Where("id = ? AND status = ?", id, "pending").
		Updates(map[string]any{"status": "completing", "expires_at": leaseUntil})
	if res.Error != nil {
		return false, fmt.Errorf("claim upload completion: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

func (s *Store) ClaimSessionCleanup(ctx context.Context, id, from string, leaseUntil time.Time) (bool, error) {
	res := s.db.WithContext(ctx).Model(&UploadSessionModel{}).
		Where("id = ? AND status = ?", id, from).
		Updates(map[string]any{"status": "cleaning", "expires_at": leaseUntil})
	if res.Error != nil {
		return false, fmt.Errorf("claim upload cleanup: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
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
