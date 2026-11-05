package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"omnirouter/internal/app"
	"omnirouter/internal/config"
	"omnirouter/internal/version"
)

func main() {
	showVersion := flag.Bool("version", false, "print OmniRouter version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("OmniRouter %s (commit %s)\n", version.Version, version.Commit)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("load configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	application, err := app.New(cfg, logger)
	if err != nil {
		logger.Error("initialize application", "error", err)
		os.Exit(1)
	}
	if err := application.Run(ctx); err != nil {
		logger.Error("application stopped", "error", err)
		os.Exit(1)
	}
}
