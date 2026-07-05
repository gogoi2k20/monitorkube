# MonitorKube

MonitorKube automatically discovers Kubernetes Services and continuously probes their health endpoints, exposing Prometheus-compatible metrics.

## Features

- Kubernetes Service discovery
- Automatic health endpoint detection
- HTTP health probing
- Prometheus metrics

## Roadmap

- [ ] Service discovery
- [ ] HTTP probing
- [ ] Metrics
- [ ] UI
- [ ] Distributed workers

## Project Skeleton

monitorkube/
│
├── cmd/
│   └── monitorkube/
│       └── main.go
│
├── internal/
│   ├── discovery/
│   ├── probe/
│   ├── metrics/
│   └── config/
│
├── deploy/
│
├── examples/
│
├── docs/
│
├── .gitignore
├── README.md
├── Makefile
└── go.mod