package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/saker-ai/filehub"
)

func main() {
	var configPath string
	fs := flag.NewFlagSet("filehub", flag.ExitOnError)
	fs.StringVar(&configPath, "config", "", "Path to filehub config JSON")
	_ = fs.Parse(os.Args[1:])

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := filehub.Run(ctx, configPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
