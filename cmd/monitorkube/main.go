package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/gaurav2k20/monitorkube/internal/app"
	"github.com/gaurav2k20/monitorkube/internal/config"
	"github.com/gaurav2k20/monitorkube/internal/logger"
)

func main() {
	log := logger.New()
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a := app.New(cfg, log)
	if err := a.Run(ctx); err != nil {
		log.Error("Application exited with error", "error", err)
		os.Exit(1)
	}
}
