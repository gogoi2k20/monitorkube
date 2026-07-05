// Package app wires together config, Kubernetes discovery, probing, and
// metrics exposition into one runnable service.
package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"

	"github.com/gaurav2k20/monitorkube/internal/config"
	"github.com/gaurav2k20/monitorkube/internal/discovery"
	"github.com/gaurav2k20/monitorkube/internal/k8sclient"
	"github.com/gaurav2k20/monitorkube/internal/metrics"
	"github.com/gaurav2k20/monitorkube/internal/probe"
)

// App owns the lifecycle of every MonitorKube component.
type App struct {
	cfg *config.Config
	log *slog.Logger
}

// New builds an App from config and a logger.
func New(cfg *config.Config, log *slog.Logger) *App {
	return &App{cfg: cfg, log: log}
}

// Run starts discovery, probing, and the metrics HTTP server, and blocks
// until ctx is cancelled or a component fails. Kept dependency-free
// (no errgroup) by hand-rolling a small fan-in of goroutine errors —
// straightforward enough at four goroutines that pulling in a library
// isn't worth it.
func (a *App) Run(ctx context.Context) error {
	a.log.Info("MonitorKube is starting",
		"cluster", a.cfg.ClusterName,
		"namespace_filter", nonEmpty(a.cfg.Namespace, "<all>"),
		"probe_interval", a.cfg.ProbeInterval.String(),
		"metrics_addr", a.cfg.MetricsAddr,
	)

	client, err := k8sclient.New(a.cfg)
	if err != nil {
		return err
	}

	reg := metrics.New()
	watcher := discovery.NewWatcher(client, a.cfg, a.log)
	manager := probe.NewManager(watcher, reg, a.cfg, a.log)

	mux := http.NewServeMux()
	mux.Handle("/metrics", reg.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	server := &http.Server{Addr: a.cfg.MetricsAddr, Handler: mux}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 4)
	var wg sync.WaitGroup

	run := func(fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil && !errors.Is(err, context.Canceled) {
				errCh <- err
				cancel() // one failure brings the rest down together
			}
		}()
	}

	run(func() error { return watcher.Run(runCtx) })
	run(func() error { return manager.Run(runCtx) })
	run(func() error {
		a.log.Info("metrics server listening", "addr", a.cfg.MetricsAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
	run(func() error {
		<-runCtx.Done()
		return server.Close()
	})

	wg.Wait()
	close(errCh)

	if err := <-errCh; err != nil {
		return err
	}
	a.log.Info("MonitorKube shut down cleanly")
	return nil
}

func nonEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
