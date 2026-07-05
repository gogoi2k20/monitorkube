// Package config loads MonitorKube's runtime configuration from environment
// variables. Kept deliberately simple for the skeleton stage — a real
// deployment will mostly rely on defaults plus CLUSTER_NAME.
package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds everything the app needs to run.
type Config struct {
	// ClusterName is attached as a label to every metric so that a single
	// VictoriaMetrics/Prometheus instance can distinguish services coming
	// from different clusters. This is the piece bare Blackbox Exporter
	// setups usually have to bolt on manually via external_labels.
	ClusterName string

	// Namespace restricts discovery to a single namespace. Empty string
	// means "watch all namespaces".
	Namespace string

	// AnnotationEnable is the Service annotation that opts a service into
	// probing, e.g. `monitorkube.io/probe: "true"`.
	AnnotationEnable string
	// AnnotationPath overrides the health check path (default "/healthz").
	AnnotationPath string
	// AnnotationPort overrides which named/numbered port to probe.
	AnnotationPort string
	// AnnotationScheme overrides http/https (default "http").
	AnnotationScheme string

	// ProbeInterval is how often each discovered target is probed.
	ProbeInterval time.Duration
	// ProbeTimeout bounds a single probe attempt.
	ProbeTimeout time.Duration

	// MetricsAddr is where /metrics is exposed for Prometheus/VictoriaMetrics
	// to scrape (pull model). Remote-write push support can be layered on
	// top of the same probe results later.
	MetricsAddr string

	// KubeAPIServer / KubeToken / KubeCACertPath let the client talk to the
	// API server. In-cluster defaults are auto-detected; these env vars
	// exist mainly for local development against a remote cluster.
	KubeAPIServer  string
	KubeToken      string
	KubeCACertPath string
	KubeInsecure   bool
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func getEnvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

// Load reads configuration from the environment, applying sane defaults so
// the app is runnable with zero configuration inside a cluster.
func Load() *Config {
	return &Config{
		ClusterName: getEnv("CLUSTER_NAME", "default"),
		Namespace:   getEnv("WATCH_NAMESPACE", ""),

		AnnotationEnable: getEnv("ANNOTATION_ENABLE", "monitorkube.io/probe"),
		AnnotationPath:   getEnv("ANNOTATION_PATH", "monitorkube.io/path"),
		AnnotationPort:   getEnv("ANNOTATION_PORT", "monitorkube.io/port"),
		AnnotationScheme: getEnv("ANNOTATION_SCHEME", "monitorkube.io/scheme"),

		ProbeInterval: getEnvDuration("PROBE_INTERVAL", 15*time.Second),
		ProbeTimeout:  getEnvDuration("PROBE_TIMEOUT", 5*time.Second),

		MetricsAddr: getEnv("METRICS_ADDR", ":9469"),

		KubeAPIServer:  getEnv("KUBE_API_SERVER", ""),
		KubeToken:      getEnv("KUBE_TOKEN", ""),
		KubeCACertPath: getEnv("KUBE_CA_CERT_PATH", ""),
		KubeInsecure:   getEnvBool("KUBE_INSECURE_SKIP_VERIFY", false),
	}
}
