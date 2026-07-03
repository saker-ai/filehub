package filehub

import (
	"context"
	"fmt"

	"github.com/saker-ai/filehub/pkg/config"
	"github.com/saker-ai/filehub/pkg/server"
)

func Run(ctx context.Context, configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("filehub config: %w", err)
	}
	srv, err := server.New(ctx, cfg)
	if err != nil {
		return fmt.Errorf("filehub server: %w", err)
	}
	return srv.Start(ctx)
}
