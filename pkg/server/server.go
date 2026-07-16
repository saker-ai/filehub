package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/saker-ai/filehub/pkg/api"
	"github.com/saker-ai/filehub/pkg/config"
	filehubnotify "github.com/saker-ai/filehub/pkg/notify"
	"github.com/saker-ai/filehub/pkg/processing"
	"github.com/saker-ai/filehub/pkg/storage"
	"github.com/saker-ai/filehub/pkg/store"
	"github.com/saker-ai/filehub/pkg/store/gormstore"
)

type Server struct {
	cfg      config.Config
	http     *http.Server
	db       *gormstore.Store
	blobs    *storage.Store
	metrics  *api.Metrics
	pipeline *processing.Pipeline
	logger   *slog.Logger
}

func New(ctx context.Context, cfg config.Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel(cfg.LogLevel)}))
	slog.SetDefault(logger)
	logSecurityWarnings(logger, cfg)
	db, err := gormstore.Open(ctx, cfg.DSN)
	if err != nil {
		return nil, err
	}
	blobs, err := storage.New(ctx, cfg)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	metrics := api.NewMetrics()
	pipeline := processing.New(cfg.ProcessingConcurrency, blobs, db, logger)
	pipeline.ObserveProcessing(metrics.ObserveProcessing)
	reviewCreatedHook, err := filehubnotify.NewReviewCreatedHook(cfg.WebHubNotify, logger)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("build review-created hook: %w", err)
	}
	router := api.NewRouter(api.RouterDeps{
		Config: cfg, Assets: db, Uploads: db, AIReviews: db, Reviews: db, Storage: blobs, Pipeline: pipeline, Metrics: metrics,
		ReviewCreatedHook: reviewCreatedHook,
	})
	return &Server{
		cfg:      cfg,
		http:     &http.Server{Addr: cfg.Addr, Handler: router, ReadHeaderTimeout: 10 * time.Second},
		db:       db,
		blobs:    blobs,
		metrics:  metrics,
		pipeline: pipeline,
		logger:   logger,
	}, nil
}

func logSecurityWarnings(logger *slog.Logger, cfg config.Config) {
	if cfg.APIKeyAuthEnabled {
		for _, key := range cfg.APIKeys {
			if key == "dev-filehub-key" {
				logger.Warn("default API key is enabled; set FILEHUB_API_KEYS before production use")
				break
			}
		}
	}
	if cfg.PresignSecret == "filehub-presign-secret" {
		logger.Warn("default presign secret is enabled; set FILEHUB_PRESIGN_SECRET before production use")
	}
	for _, origin := range cfg.CORSOrigins {
		if origin == "*" {
			logger.Warn("CORS allows all origins; set FILEHUB_CORS_ORIGINS before production use")
			break
		}
	}
	if cfg.PresignTTL >= 7*24*time.Hour {
		logger.Warn("presigned URL TTL is long", "ttl", cfg.PresignTTL.String())
	}
}

func (s *Server) Start(ctx context.Context) error {
	registration := startServiceDiscovery(ctx, s.logger, s.cfg)
	defer func() {
		if registration != nil {
			_ = registration.Stop(context.Background())
		}
	}()
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("filehub starting", "addr", s.cfg.Addr, "dsn", s.cfg.DSN, "backend", s.cfg.Storage.Backend)
		err := s.http.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()
	go s.runGC(ctx)
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return s.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (s *Server) runGC(ctx context.Context) {
	interval := s.cfg.GCInterval
	if interval <= 0 {
		interval = time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.collectExpired(ctx)
		}
	}
}

func (s *Server) collectExpired(ctx context.Context) {
	for {
		assets, err := s.db.ListExpired(ctx, time.Now().UTC(), 100)
		if err != nil {
			s.logger.Warn("asset gc list failed", "error", err)
			return
		}
		for _, asset := range assets {
			if err := s.deleteAsset(ctx, asset); err != nil {
				s.logger.Warn("asset gc delete failed", "asset_id", asset.ID, "error", err)
			}
		}
		if len(assets) < 100 {
			break
		}
	}
	sessions, err := s.db.ListExpiredSessions(ctx, time.Now().UTC(), 100)
	if err != nil {
		s.logger.Warn("upload gc list failed", "error", err)
		return
	}
	for _, session := range sessions {
		claimed, err := s.db.TransitionSessionStatus(ctx, session.ID, session.Status, "cleaning")
		if err != nil {
			s.logger.Warn("upload gc claim failed", "upload_id", session.ID, "error", err)
			continue
		}
		if !claimed {
			continue
		}
		if session.ProviderUploadID != "" {
			if err := s.blobs.AbortMultipartUpload(ctx, session.StorageKey, session.ProviderUploadID); err != nil {
				s.logger.Warn("upload multipart abort failed", "upload_id", session.ID, "error", err)
			}
		}
		if session.Mode == "direct" && session.StorageKey != "" {
			if err := s.blobs.Delete(ctx, session.StorageKey); err != nil {
				s.logger.Warn("upload direct object gc failed", "upload_id", session.ID, "error", err)
			}
		}
		if err := s.blobs.DeleteRecursive(ctx, storage.ChunkPrefix(session.ID)); err != nil {
			s.logger.Warn("upload chunk gc failed", "upload_id", session.ID, "error", err)
		}
		if err := s.db.DeleteSession(ctx, session.ID); err != nil {
			s.logger.Warn("upload session gc failed", "upload_id", session.ID, "error", err)
		} else if session.Mode == "direct" || session.Mode == "direct_multipart" {
			s.metrics.RecordDirectUpload(session.Mode, "orphan_cleaned")
		}
	}
}

func (s *Server) deleteAsset(ctx context.Context, asset *store.Asset) error {
	var out error
	if err := s.blobs.Delete(ctx, asset.StorageKey); err != nil {
		out = errors.Join(out, err)
	}
	if err := s.blobs.DeleteRecursive(ctx, "_thumbs/"+asset.ID+"/"); err != nil {
		out = errors.Join(out, err)
	}
	if err := s.db.Delete(ctx, asset.TenantID, asset.ID); err != nil {
		out = errors.Join(out, err)
	}
	return out
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("filehub shutting down")
	var out error
	if err := s.http.Shutdown(ctx); err != nil {
		out = errors.Join(out, fmt.Errorf("http shutdown: %w", err))
	}
	done := make(chan struct{})
	go func() {
		s.pipeline.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		out = errors.Join(out, ctx.Err())
	}
	if err := s.db.Close(); err != nil {
		out = errors.Join(out, fmt.Errorf("database close: %w", err))
	}
	return out
}

func logLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
