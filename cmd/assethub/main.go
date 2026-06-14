package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/saker-ai/assethub"
)

func main() {
	var configPath string
	fs := flag.NewFlagSet("assethub", flag.ExitOnError)
	fs.StringVar(&configPath, "config", "", "Path to assethub config JSON")
	_ = fs.Parse(os.Args[1:])

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := assethub.Run(ctx, configPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
