package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"time"

	"github.com/cloud-idaas/idaas-akless-sidecar/internal/projectedauth"
)

type options struct {
	configPath, listenAddr, defaultRoleARN, allowedRoleARNs string
	refreshTimeout                                          time.Duration
	refreshMinValidity                                      time.Duration
	shutdownTimeout                                         time.Duration
	authTokenFile                                           string
	authEnabled                                             bool
	healthcheck                                             bool
	version                                                 bool
}

// CLI > environment > defaults. SDK JSON fields retain their original names.
func parseOptions(args []string, getenv func(string) string, output io.Writer) (options, error) {
	var o options
	envOr := func(key, fallback string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return fallback
	}
	fs := flag.NewFlagSet("idaas-akless-sidecar", flag.ContinueOnError)
	fs.SetOutput(output)
	var timeoutValue string
	var minValidityValue string
	var authEnabledValue string
	var portValue string
	var shutdownValue string
	fs.StringVar(&o.configPath, "config", envOr("CLOUD_IDAAS_CONFIG_PATH", "/etc/idaas-akless/client-config.json"), "Core SDK config file (CLOUD_IDAAS_CONFIG_PATH)")
	fs.StringVar(&o.authTokenFile, "credential-auth-token-file", envOr("IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE", projectedauth.DefaultTokenFile), "read-only projected credential authentication token (IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE)")
	fs.StringVar(&authEnabledValue, "credential-auth-enabled", envOr("IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED", "true"), "require local token authentication: true or false; false removes local SSRF protection (IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED)")
	fs.StringVar(&portValue, "listen-port", envOr("IDAAS_AKLESS_SIDECAR_LISTEN_PORT", defaultListenPort), "port on fixed 127.0.0.1, from 1 to 65535 (IDAAS_AKLESS_SIDECAR_LISTEN_PORT)")
	fs.StringVar(&o.defaultRoleARN, "default-role-arn", envOr("IDAAS_AKLESS_DEFAULT_ROLE_ARN", ""), "fallback role identifier (IDAAS_AKLESS_DEFAULT_ROLE_ARN)")
	fs.StringVar(&o.allowedRoleARNs, "allowed-role-arns", envOr("IDAAS_AKLESS_ALLOWED_ROLE_ARNS", ""), "optional comma-separated role identifiers; empty disables local allowlist (IDAAS_AKLESS_ALLOWED_ROLE_ARNS)")
	fs.StringVar(&timeoutValue, "credential-refresh-timeout", envOr("IDAAS_AKLESS_CREDENTIAL_REFRESH_TIMEOUT", "10s"), "total refresh budget including IDaaS token acquisition and Developer API, with unit, e.g. 10s (IDAAS_AKLESS_CREDENTIAL_REFRESH_TIMEOUT)")
	fs.StringVar(&minValidityValue, "credential-refresh-min-validity", envOr("IDAAS_AKLESS_CREDENTIAL_REFRESH_MIN_VALIDITY", "5m"), "cached credentials must have more remaining lifetime to return without waiting for refresh; align with SDK refresh window (IDAAS_AKLESS_CREDENTIAL_REFRESH_MIN_VALIDITY)")
	fs.BoolVar(&o.healthcheck, "healthcheck", false, "GET own /healthz; reads no SDK config and initializes no SDK")
	fs.BoolVar(&o.version, "version", false, "print build version and revision without SDK initialization")
	fs.StringVar(&shutdownValue, "shutdown-timeout", envOr("IDAAS_AKLESS_SHUTDOWN_TIMEOUT", "auto"), "HTTP drain budget: auto uses refresh timeout + 5s; explicit duration must be at least that budget (IDAAS_AKLESS_SHUTDOWN_TIMEOUT)")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() > 0 {
		return o, fmt.Errorf("unexpected positional arguments")
	}
	if o.version {
		return o, nil
	}
	port, err := strconv.Atoi(portValue)
	if err != nil || port < 1 || port > 65535 {
		return o, fmt.Errorf("listen-port must be an integer from 1 to 65535")
	}
	// Neither service startup nor health probes accept a configurable bind host.
	o.listenAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	// Health probes depend only on the port, never upstream settings.
	if o.healthcheck {
		return o, nil
	}
	if o.configPath == "" {
		return o, fmt.Errorf("config path must not be empty")
	}
	switch authEnabledValue {
	case "true":
		o.authEnabled = true
	case "false":
		o.authEnabled = false
	default:
		return o, fmt.Errorf("credential-auth-enabled must be true or false")
	}
	if o.authEnabled && !filepath.IsAbs(o.authTokenFile) {
		return o, fmt.Errorf("credential-auth-token-file must be an absolute path")
	}
	o.refreshTimeout, err = time.ParseDuration(timeoutValue)
	if err != nil || o.refreshTimeout <= 0 {
		return o, fmt.Errorf("credential-refresh-timeout must be a positive duration with unit (e.g. 10s)")
	}
	o.refreshMinValidity, err = time.ParseDuration(minValidityValue)
	if err != nil || o.refreshMinValidity <= 0 {
		return o, fmt.Errorf("credential-refresh-min-validity must be a positive duration with unit (e.g. 5m)")
	}
	o.shutdownTimeout = responseWriteTimeout(o.refreshTimeout)
	if shutdownValue != "auto" {
		o.shutdownTimeout, err = time.ParseDuration(shutdownValue)
		if err != nil || o.shutdownTimeout < responseWriteTimeout(o.refreshTimeout) {
			return o, fmt.Errorf("shutdown-timeout must be auto or a duration at least credential-refresh-timeout + 5s")
		}
	}
	return o, nil
}
