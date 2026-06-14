package assethub

import (
	"context"
	"fmt"

	"github.com/saker-ai/assethub/pkg/config"
	"github.com/saker-ai/assethub/pkg/server"
)

func Run(ctx context.Context, configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("assethub config: %w", err)
	}
	srv, err := server.New(ctx, cfg)
	if err != nil {
		return fmt.Errorf("assethub server: %w", err)
	}
	return srv.Start(ctx)
}
