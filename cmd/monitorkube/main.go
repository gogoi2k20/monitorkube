package main

import (
	"github.com/gaurav2k20/monitorkube/internal/app"
	"github.com/gaurav2k20/monitorkube/internal/logger"
)

func main() {
	log := logger.New()

	app := app.New(log)

	if err := app.Run(); err != nil {
		log.Error("Application exited", "error", err)
	}
}
