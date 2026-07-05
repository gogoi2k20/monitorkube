// Package probe runs health checks against discovered targets and
// records the results as Prometheus-format metrics. It intentionally
// does its own lightweight HTTP probing rather than shelling out to
// Blackbox Exporter — for a v0 skeleton this keeps the moving parts down
// to one binary. Swapping this out to delegate to a running Blackbox
// Exporter instance (via its /probe?target=...&module=... endpoint) is a
// natural follow-up if you want TCP/ICMP/TLS-cert-expiry checks without
// reimplementing them.
package probe

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gaurav2k20/monitorkube/internal/config"
	"github.com/gaurav2k20/monitorkube/internal/discovery"
	"github.com/gaurav2k20/monitorkube/internal/metrics"
)

// TargetSource is anything that can hand back the current set of probe
// targets. discovery.Watcher satisfies this; tests can fake it easily.
type TargetSource interface {
	Targets() []discovery.Target
}

// Manager owns one goroutine per currently-known target and keeps that
// set in sync with what TargetSource reports, at cfg.ProbeInterval
// resolution.
type Manager struct {
	source  TargetSource
	metrics *metrics.Registry
	cfg     *config.Config
	log     *slog.Logger
	client  *http.Client

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	active  map[string]discovery.Target // last known Target per key, needed to clean up its metrics on removal
}

// NewManager builds a probe Manager.
func NewManager(source TargetSource, m *metrics.Registry, cfg *config.Config, log *slog.Logger) *Manager {
	return &Manager{
		source:  source,
		metrics: m,
		cfg:     cfg,
		log:     log,
		client:  &http.Client{Timeout: cfg.ProbeTimeout},
		cancels: make(map[string]context.CancelFunc),
		active:  make(map[string]discovery.Target),
	}
}

// Run reconciles the set of running probe goroutines against discovered
// targets every reconcileInterval, until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) error {
	const reconcileInterval = 5 * time.Second
	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()

	m.reconcile(ctx)
	for {
		select {
		case <-ctx.Done():
			m.stopAll()
			return ctx.Err()
		case <-ticker.C:
			m.reconcile(ctx)
		}
	}
}

func (m *Manager) reconcile(ctx context.Context) {
	current := m.source.Targets()
	seen := make(map[string]struct{}, len(current))

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, t := range current {
		key := t.Key()
		seen[key] = struct{}{}
		m.active[key] = t
		if _, running := m.cancels[key]; running {
			continue
		}
		probeCtx, cancel := context.WithCancel(ctx)
		m.cancels[key] = cancel
		go m.probeLoop(probeCtx, t)
	}

	for key, cancel := range m.cancels {
		if _, stillPresent := seen[key]; !stillPresent {
			cancel()
			delete(m.cancels, key)
			if t, ok := m.active[key]; ok {
				m.metrics.Delete(t)
				delete(m.active, key)
			}
		}
	}

	m.metrics.SetDiscovered(len(seen))
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cancel := range m.cancels {
		cancel()
	}
	m.cancels = make(map[string]context.CancelFunc)
}

func (m *Manager) probeLoop(ctx context.Context, t discovery.Target) {
	ticker := time.NewTicker(m.cfg.ProbeInterval)
	defer ticker.Stop()

	m.doProbe(ctx, t) // probe immediately on discovery, don't wait for first tick
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.doProbe(ctx, t)
		}
	}
}

func (m *Manager) doProbe(ctx context.Context, t discovery.Target) {
	reqCtx, cancel := context.WithTimeout(ctx, m.cfg.ProbeTimeout)
	defer cancel()

	start := time.Now()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, t.URL, nil)
	success := 0.0
	if err == nil {
		resp, err2 := m.client.Do(req)
		if err2 == nil {
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 400 {
				success = 1.0
			}
		} else {
			err = err2
		}
	}
	duration := time.Since(start).Seconds()

	m.metrics.SetSuccess(t, success)
	m.metrics.SetDuration(t, duration)

	if success == 1.0 {
		m.log.Debug("probe succeeded", "target", t.URL, "duration_s", duration)
	} else {
		m.log.Warn("probe failed", "target", t.URL, "error", err, "duration_s", duration)
	}
}
