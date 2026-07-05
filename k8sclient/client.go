// Package k8sclient is a deliberately small Kubernetes API client.
//
// We avoid client-go here on purpose: it pulls in a large dependency tree
// rooted at k8s.io module paths, which is more than a Service-watching
// sidecar needs. This client only knows how to do the two things
// MonitorKube actually requires: authenticate, and list/watch Services.
// It's a fine trade-off for a focused tool; swap to client-go later if
// the scope grows (e.g. watching CRDs, leader election, etc.).
package k8sclient

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gaurav2k20/monitorkube/internal/config"
)

const (
	inClusterCACert = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	inClusterToken  = "/var/run/secrets/kubernetes.io/serviceaccount/token"
)

// Client is a thin wrapper around http.Client pre-configured with the
// Kubernetes API server address and auth token.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New builds a Client, preferring in-cluster service account credentials
// and falling back to explicit config (useful for local development
// against a remote cluster via `kubectl proxy` or a service account
// token you've copied out).
func New(cfg *config.Config) (*Client, error) {
	if cfg.KubeAPIServer != "" {
		return newFromExplicitConfig(cfg)
	}
	return newInCluster(cfg)
}

func newInCluster(cfg *config.Config) (*Client, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, fmt.Errorf("not running in-cluster (KUBERNETES_SERVICE_HOST/PORT unset) and KUBE_API_SERVER not set")
	}

	tokenBytes, err := os.ReadFile(inClusterToken)
	if err != nil {
		return nil, fmt.Errorf("reading service account token: %w", err)
	}

	caPool := x509.NewCertPool()
	caBytes, err := os.ReadFile(inClusterCACert)
	if err != nil {
		return nil, fmt.Errorf("reading service account CA cert: %w", err)
	}
	if !caPool.AppendCertsFromPEM(caBytes) {
		return nil, fmt.Errorf("failed to parse in-cluster CA cert")
	}

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: caPool},
		},
	}

	return &Client{
		BaseURL: fmt.Sprintf("https://%s", net.JoinHostPort(host, port)),
		Token:   strings.TrimSpace(string(tokenBytes)),
		HTTP:    httpClient,
	}, nil
}

func newFromExplicitConfig(cfg *config.Config) (*Client, error) {
	tlsCfg := &tls.Config{InsecureSkipVerify: cfg.KubeInsecure} //nolint:gosec // opt-in via config for local dev

	if cfg.KubeCACertPath != "" {
		caPool := x509.NewCertPool()
		caBytes, err := os.ReadFile(cfg.KubeCACertPath)
		if err != nil {
			return nil, fmt.Errorf("reading CA cert: %w", err)
		}
		if !caPool.AppendCertsFromPEM(caBytes) {
			return nil, fmt.Errorf("failed to parse CA cert at %s", cfg.KubeCACertPath)
		}
		tlsCfg.RootCAs = caPool
	}

	return &Client{
		BaseURL: strings.TrimRight(cfg.KubeAPIServer, "/"),
		Token:   cfg.KubeToken,
		HTTP: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
		},
	}, nil
}

// NewRequest builds an authenticated request against the API server.
func (c *Client) NewRequest(method, path string) (*http.Request, error) {
	req, err := http.NewRequest(method, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}
