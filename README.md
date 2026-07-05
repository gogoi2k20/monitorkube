# MonitorKube

MonitorKube automatically discovers Kubernetes Services and continuously
probes their health endpoints, exposing Prometheus/VictoriaMetrics-compatible
metrics — no static Blackbox Exporter target list, no per-service Probe CRD.

## Why

Blackbox Exporter is great at probing, but it only knows about the targets
you tell it about. Keeping that list in sync with a Kubernetes cluster where
services come and go is manual, ongoing work. MonitorKube closes that gap:

1. **Discovers** Services opted in via a `monitorkube.io/probe: "true"` annotation.
2. **Probes** them on an interval and records success/duration as metrics.
3. **Labels** every metric with `service`, `namespace`, `cluster`, and `target`
   — so one central VictoriaMetrics/Prometheus can tell clusters and services
   apart without hand-written relabeling rules.

## Status: early skeleton, functional end-to-end

This is a working v0, not a placeholder. What's implemented:

- [x] Service discovery (list + watch via the Kubernetes API, annotation-filtered)
- [x] HTTP probing (per-target goroutines, configurable interval/timeout)
- [x] Metrics (`svc_probe_success`, `svc_probe_duration_seconds`, `monitorkube_discovered_services`)
- [x] `/metrics` and `/healthz` HTTP endpoints
- [x] RBAC + Deployment manifests, Dockerfile

Not yet implemented (see Roadmap):

- [ ] Remote-write push to VictoriaMetrics (currently pull/scrape only)
- [ ] Non-HTTP probes (TCP, ICMP, TLS cert expiry) — could delegate to a
      running Blackbox Exporter instance instead of reimplementing these
- [ ] UI
- [ ] Distributed/multi-replica probing (currently single replica)
- [ ] kubeconfig file parsing for local dev (currently: in-cluster auto-detect,
      or explicit `KUBE_API_SERVER`/`KUBE_TOKEN` env vars)

## Quick start

```bash
make build
./bin/monitorkube   # reads config from env vars, see below
```

Outside a cluster, set at minimum:

```bash
export KUBE_API_SERVER=https://<api-server>:6443
export KUBE_TOKEN=<a-token-with-service-read-access>
```

Inside a cluster, no configuration is required beyond the ServiceAccount's
default RBAC permissions (see `deploy/rbac.yaml`).

## Configuration (environment variables)

| Variable | Default | Description |
|---|---|---|
| `CLUSTER_NAME` | `default` | Attached to every metric as the `cluster` label |
| `WATCH_NAMESPACE` | `` (all) | Restrict discovery to one namespace |
| `ANNOTATION_ENABLE` | `monitorkube.io/probe` | Annotation that opts a service in |
| `ANNOTATION_PATH` | `monitorkube.io/path` | Overrides probe path (default `/healthz`) |
| `ANNOTATION_PORT` | `monitorkube.io/port` | Overrides probe port (default: service's first port) |
| `ANNOTATION_SCHEME` | `monitorkube.io/scheme` | Overrides http/https (default `http`) |
| `PROBE_INTERVAL` | `15s` | How often each target is probed |
| `PROBE_TIMEOUT` | `5s` | Per-probe timeout |
| `METRICS_ADDR` | `:9469` | Where `/metrics` and `/healthz` are served |
| `KUBE_API_SERVER` | in-cluster | Explicit API server URL for local dev |
| `KUBE_TOKEN` | in-cluster | Explicit bearer token for local dev |

## Opting a service in

```yaml
metadata:
  annotations:
    monitorkube.io/probe: "true"
```

See `examples/annotated-service.yaml` for the full example.

## Deploying

```bash
kubectl apply -f deploy/rbac.yaml
kubectl apply -f deploy/deployment.yaml
# optional, if you run Prometheus Operator:
kubectl apply -f deploy/servicemonitor.yaml
```

## Project layout

```
monitorkube/
├── cmd/monitorkube/       entrypoint
├── internal/
│   ├── app/               wires everything together, owns the run loop
│   ├── config/             env-var configuration
│   ├── discovery/         watches Services, filters by annotation, builds Targets
│   ├── k8sclient/         minimal Kubernetes API client (no client-go dependency)
│   ├── metrics/           Prometheus text-exposition + in-memory sample store
│   ├── probe/             per-target probe goroutines
│   └── logger/            structured JSON logging
├── deploy/                RBAC, Deployment, ServiceMonitor manifests
├── examples/              example annotated Service
├── Dockerfile
└── Makefile
```

### A design note on dependencies

This skeleton intentionally has **zero external Go dependencies**. Two
deliberate choices got us there:

- **No `client-go`.** It's the idiomatic choice for anything Kubernetes-native,
  but it's a large dependency tree, and this tool only needs to list/watch one
  resource type (Services). `internal/k8sclient` is a ~100-line REST client
  that does exactly that.
- **No `prometheus/client_golang`.** `internal/metrics` hand-writes the
  Prometheus text-exposition format instead. If your build environment has
  full access to `proxy.golang.org`, swapping this for the official client
  library is a clean, isolated change — nothing outside `internal/metrics`
  needs to know the difference.

Both are reasonable to revisit as the project matures; they were the right
call for getting a real, compiling v0 running fast.
