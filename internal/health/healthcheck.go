package health

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// HealthcheckTimeout must be shorter than the deployment's 4 s exec probe deadline.
const HealthcheckTimeout = 2 * time.Second

// Run checks /healthz for HTTP 200 and an "ok" body without SDK initialization.
// The command entry point always supplies its fixed 127.0.0.1 listener address.
func Run(listenAddr string, timeout time.Duration) error {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", listenAddr, err)
	}
	// Probe wildcard listeners through loopback.
	switch strings.ToLower(host) {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	url := "http://" + net.JoinHostPort(host, port) + "/healthz"

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request for %s: %w", url, err)
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return fmt.Errorf("get %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read at most 64 bytes from the health response.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return fmt.Errorf("read %s response: %w", url, err)
	}
	trimmed := strings.TrimSpace(string(body))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned HTTP %d: %s", url, resp.StatusCode, trimmed)
	}
	if trimmed != "ok" {
		return fmt.Errorf("%s returned unexpected body %q (want \"ok\")", url, trimmed)
	}
	return nil
}
