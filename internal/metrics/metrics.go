// Package metrics implements a minimal Prometheus text-exposition
// endpoint by hand, using only the standard library.
//
// Why not github.com/prometheus/client_golang? It transitively depends
// on google.golang.org/protobuf and golang.org/x/sys, both of which sit
// behind vanity import domains that may be firewalled off in locked-down
// build environments (as they were in the sandbox this skeleton was
// built in). Hand-rolling the (simple) text format keeps this package
// buildable with zero external dependencies anywhere. If your build
// environment has full network access, swapping this package for
// client_golang later is a clean, isolated change — nothing outside
// this package needs to know the difference.
package metrics

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/gaurav2k20/monitorkube/internal/discovery"
)

// Registry holds the current value of every metric MonitorKube exposes.
//
// The metric is deliberately named "svc_probe_success" rather than
// "probe_success" — that name already belongs to Blackbox Exporter's
// convention, and running both side by side (a realistic transition
// path) would cause confusing metric collisions.
type Registry struct {
	mu sync.RWMutex

	// keyed by target Key() ("namespace/service")
	success  map[string]sample
	duration map[string]sample

	discoveredCount float64
}

type sample struct {
	labels map[string]string
	value  float64
}

// New builds an empty Registry.
func New() *Registry {
	return &Registry{
		success:  make(map[string]sample),
		duration: make(map[string]sample),
	}
}

func labelsFor(t discovery.Target) map[string]string {
	return map[string]string{
		"service":   t.Service,
		"namespace": t.Namespace,
		"cluster":   t.Cluster,
		"target":    t.URL,
	}
}

// SetSuccess records whether the last probe of t succeeded (1) or failed (0).
func (r *Registry) SetSuccess(t discovery.Target, v float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.success[t.Key()] = sample{labels: labelsFor(t), value: v}
}

// SetDuration records how long the last probe of t took, in seconds.
func (r *Registry) SetDuration(t discovery.Target, seconds float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.duration[t.Key()] = sample{labels: labelsFor(t), value: seconds}
}

// Delete removes all metrics for a target that's no longer discovered,
// so it stops showing up in /metrics instead of reporting stale values
// forever.
func (r *Registry) Delete(t discovery.Target) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.success, t.Key())
	delete(r.duration, t.Key())
}

// SetDiscovered records how many services are currently being probed.
func (r *Registry) SetDiscovered(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.discoveredCount = float64(n)
}

// Handler returns the /metrics HTTP handler for Prometheus/VictoriaMetrics
// to scrape (pull model). A future remote-write pusher can read the same
// underlying samples without changing the probe manager.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		r.mu.RLock()
		defer r.mu.RUnlock()

		writeMetric(w, "svc_probe_success",
			"Whether the last probe of a Kubernetes service succeeded (1) or failed (0).",
			"gauge", r.success)
		writeMetric(w, "svc_probe_duration_seconds",
			"Duration in seconds of the last probe against a Kubernetes service.",
			"gauge", r.duration)

		fmt.Fprintf(w, "# HELP monitorkube_discovered_services Number of services currently discovered and being probed.\n")
		fmt.Fprintf(w, "# TYPE monitorkube_discovered_services gauge\n")
		fmt.Fprintf(w, "monitorkube_discovered_services %v\n", r.discoveredCount)
	})
}

func writeMetric(w io.Writer, name, help, typ string, samples map[string]sample) {
	fmt.Fprintf(w, "# HELP %s %s\n", name, help)
	fmt.Fprintf(w, "# TYPE %s %s\n", name, typ)

	// Sort keys for stable, diffable output across scrapes.
	keys := make([]string, 0, len(samples))
	for k := range samples {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		s := samples[k]
		fmt.Fprintf(w, "%s{%s} %v\n", name, formatLabels(s.labels), s.value)
	}
}

func formatLabels(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf(`%s=%q`, k, escapeLabelValue(labels[k])))
	}
	return strings.Join(parts, ",")
}

func escapeLabelValue(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	return v
}
