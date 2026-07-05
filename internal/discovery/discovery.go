// Package discovery watches Kubernetes Services and turns the ones opted
// in via annotation into probe Targets. This is the "auto-discovery"
// half of MonitorKube: instead of hand-maintaining a static Blackbox
// Exporter target list, a service becomes monitored the moment someone
// adds `monitorkube.io/probe: "true"` to it.
package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gaurav2k20/monitorkube/internal/config"
	"github.com/gaurav2k20/monitorkube/internal/k8sclient"
)

// Target is everything the prober needs to check one service, plus the
// metadata that makes the resulting metric useful — this is the piece
// Blackbox-Exporter-style setups don't give you for free.
type Target struct {
	Service   string
	Namespace string
	Cluster   string
	URL       string
}

// Key uniquely identifies a target for diffing purposes.
func (t Target) Key() string {
	return t.Namespace + "/" + t.Service
}

// service mirrors just the fields of a Kubernetes Service we care about.
// We deliberately don't pull in k8s.io/api for one struct.
type service struct {
	Metadata struct {
		Name        string            `json:"name"`
		Namespace   string            `json:"namespace"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		ClusterIP string `json:"clusterIP"`
		Ports     []struct {
			Name string `json:"name"`
			Port int32  `json:"port"`
		} `json:"ports"`
	} `json:"spec"`
}

type serviceList struct {
	Metadata struct {
		ResourceVersion string `json:"resourceVersion"`
	} `json:"metadata"`
	Items []service `json:"items"`
}

type watchEvent struct {
	Type   string  `json:"type"` // ADDED, MODIFIED, DELETED
	Object service `json:"object"`
}

// Watcher watches Services cluster-wide (or in one namespace) and emits
// the current set of probe targets whenever it changes.
type Watcher struct {
	client *k8sclient.Client
	cfg    *config.Config
	log    *slog.Logger

	mu      sync.RWMutex
	targets map[string]Target
}

// NewWatcher builds a Watcher. Call Run to start watching; call Targets
// at any time to get a point-in-time snapshot for the probe manager.
func NewWatcher(client *k8sclient.Client, cfg *config.Config, log *slog.Logger) *Watcher {
	return &Watcher{
		client:  client,
		cfg:     cfg,
		log:     log,
		targets: make(map[string]Target),
	}
}

// Targets returns a snapshot of currently discovered targets.
func (w *Watcher) Targets() []Target {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]Target, 0, len(w.targets))
	for _, t := range w.targets {
		out = append(out, t)
	}
	return out
}

func (w *Watcher) servicesPath() string {
	if w.cfg.Namespace != "" {
		return fmt.Sprintf("/api/v1/namespaces/%s/services", w.cfg.Namespace)
	}
	return "/api/v1/services"
}

// Run performs an initial list to populate targets, then watches for
// changes until ctx is cancelled. On any watch error it backs off and
// re-lists — a simplified version of what client-go's reflector does
// under the hood.
func (w *Watcher) Run(ctx context.Context) error {
	for {
		resourceVersion, err := w.list(ctx)
		if err != nil {
			w.log.Error("service list failed, retrying", "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				continue
			}
		}

		if err := w.watch(ctx, resourceVersion); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			w.log.Warn("service watch stream ended, re-listing", "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
	}
}

func (w *Watcher) list(ctx context.Context) (string, error) {
	req, err := w.client.NewRequest(http.MethodGet, w.servicesPath())
	if err != nil {
		return "", err
	}
	req = req.WithContext(ctx)

	resp, err := w.client.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("list services: unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var list serviceList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return "", fmt.Errorf("decoding service list: %w", err)
	}

	w.mu.Lock()
	w.targets = make(map[string]Target)
	for _, svc := range list.Items {
		if t, ok := w.toTarget(svc); ok {
			w.targets[t.Key()] = t
		}
	}
	count := len(w.targets)
	w.mu.Unlock()

	w.log.Info("service discovery: initial list complete", "eligible_targets", count, "total_services", len(list.Items))

	return list.Metadata.ResourceVersion, nil
}

func (w *Watcher) watch(ctx context.Context, resourceVersion string) error {
	path := fmt.Sprintf("%s?watch=1&resourceVersion=%s", w.servicesPath(), resourceVersion)
	req, err := w.client.NewRequest(http.MethodGet, path)
	if err != nil {
		return err
	}
	req = req.WithContext(ctx)

	resp, err := w.client.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("watch services: unexpected status %d: %s", resp.StatusCode, string(body))
	}

	decoder := json.NewDecoder(resp.Body)
	for decoder.More() {
		var evt watchEvent
		if err := decoder.Decode(&evt); err != nil {
			return fmt.Errorf("decoding watch event: %w", err)
		}
		w.handleEvent(evt)
	}
	return nil
}

func (w *Watcher) handleEvent(evt watchEvent) {
	target, eligible := w.toTarget(evt.Object)
	key := evt.Object.Metadata.Namespace + "/" + evt.Object.Metadata.Name

	w.mu.Lock()
	defer w.mu.Unlock()

	switch evt.Type {
	case "DELETED":
		if _, existed := w.targets[key]; existed {
			delete(w.targets, key)
			w.log.Info("service removed from discovery", "service", key)
		}
	case "ADDED", "MODIFIED":
		if eligible {
			if _, existed := w.targets[key]; !existed {
				w.log.Info("service discovered for probing", "service", key, "url", target.URL)
			}
			w.targets[key] = target
		} else if _, existed := w.targets[key]; existed {
			delete(w.targets, key)
			w.log.Info("service no longer eligible for probing", "service", key)
		}
	}
}

// toTarget converts a Service into a Target if it opted in via the
// enable annotation. Path/port/scheme can be overridden per-service via
// annotations; otherwise sensible defaults are used.
func (w *Watcher) toTarget(svc service) (Target, bool) {
	ann := svc.Metadata.Annotations
	if ann == nil || ann[w.cfg.AnnotationEnable] != "true" {
		return Target{}, false
	}
	if svc.Spec.ClusterIP == "" || svc.Spec.ClusterIP == "None" {
		return Target{}, false // headless services have no single address to probe
	}

	scheme := ann[w.cfg.AnnotationScheme]
	if scheme == "" {
		scheme = "http"
	}

	port := ann[w.cfg.AnnotationPort]
	if port == "" && len(svc.Spec.Ports) > 0 {
		port = fmt.Sprintf("%d", svc.Spec.Ports[0].Port)
	}
	if port == "" {
		return Target{}, false
	}

	path := ann[w.cfg.AnnotationPath]
	if path == "" {
		path = "/healthz"
	}

	url := fmt.Sprintf("%s://%s.%s.svc.cluster.local:%s%s",
		scheme, svc.Metadata.Name, svc.Metadata.Namespace, port, path)

	return Target{
		Service:   svc.Metadata.Name,
		Namespace: svc.Metadata.Namespace,
		Cluster:   w.cfg.ClusterName,
		URL:       url,
	}, true
}
