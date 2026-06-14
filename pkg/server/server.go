package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/saker-ai/assethub/pkg/api"
	"github.com/saker-ai/assethub/pkg/config"
	"github.com/saker-ai/assethub/pkg/processing"
	"github.com/saker-ai/assethub/pkg/storage"
	"github.com/saker-ai/assethub/pkg/store"
	"github.com/saker-ai/assethub/pkg/store/gormstore"
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
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel(cfg.LogLevel)}))
	slog.SetDefault(logger)
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
	router := api.NewRouter(api.RouterDeps{
		Config: cfg, Assets: db, Uploads: db, Storage: blobs, Pipeline: pipeline, Metrics: metrics,
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

func (s *Server) Start(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("assethub starting", "addr", s.cfg.Addr, "dsn", s.cfg.DSN, "backend", s.cfg.Storage.Backend)
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
		if err := s.blobs.DeleteRecursive(ctx, storage.ChunkPrefix(session.ID)); err != nil {
			s.logger.Warn("upload chunk gc failed", "upload_id", session.ID, "error", err)
		}
		if err := s.db.DeleteSession(ctx, session.ID); err != nil {
			s.logger.Warn("upload session gc failed", "upload_id", session.ID, "error", err)
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
	s.logger.Info("assethub shutting down")
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
