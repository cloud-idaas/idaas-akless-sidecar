// Package main runs the Pod-local credential proxy and its health check.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/cloud-idaas/idaas-akless-sidecar/internal/credentials"
	"github.com/cloud-idaas/idaas-akless-sidecar/internal/health"
	"github.com/cloud-idaas/idaas-akless-sidecar/internal/projectedauth"
)

const (
	// Private port outside Linux's default ephemeral range (32768–60999).
	defaultListenPort = "61765"
)

// Release builds override these values with -X; local builds retain honest defaults.
var (
	version  = "dev"
	revision = "unknown"
)

func main() {
	opts, err := parseOptions(os.Args[1:], os.Getenv, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}
	if opts.version {
		fmt.Printf("idaas-akless-sidecar version=%s revision=%s\n", version, revision)
		return
	}
	configPath, listenAddr := opts.configPath, opts.listenAddr
	defaultRoleARN := opts.defaultRoleARN

	// 自检只访问本地 HTTP 服务，必须在读取配置和初始化 SDK 前返回。
	if opts.healthcheck {
		if err := health.Run(listenAddr, health.HealthcheckTimeout); err != nil {
			fmt.Fprintf(os.Stderr, "healthcheck failed: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	var auth credentials.RequestAuthenticator
	if opts.authEnabled {
		projected, err := projectedauth.NewProjected(opts.authTokenFile, projectedauth.PodContext{
			UID: os.Getenv("POD_UID"), Namespace: os.Getenv("POD_NAMESPACE"), ServiceAccount: os.Getenv("POD_SERVICE_ACCOUNT"),
		})
		if err != nil {
			log.Fatalf("initialize local credential authentication failed: %v; require root-owned read-only projected mount and valid Pod-bound token", err)
		}
		defer func() { _ = projected.Close() }()
		auth = projected
		log.Print("local credential authentication enabled")
	} else {
		auth = credentials.UnauthenticatedAccess{}
		log.Print("WARN local credential authentication disabled: localhost callers can obtain STS without a token; local SSRF credential protection is disabled")
	}
	svc, err := credentials.NewServiceWithRefreshPolicy(configPath, opts.refreshTimeout, opts.refreshMinValidity)
	if err != nil {
		log.Fatalf("initialize IDaaS Core SDK: %v", err)
	}
	allowedARNs := parseAllowedARNs(opts.allowedRoleARNs)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", health.HealthzHandler)
	mux.HandleFunc("/credentials", svc.CredentialsHandler(defaultRoleARN, allowedARNs, auth))

	srv := newHTTPServer(listenAddr, mux, opts.refreshTimeout)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	log.Printf("idaas-akless-sidecar listening on %s, default roleArn=%s, shutdown budget=%s",
		listenAddr, credentials.MaskARN(defaultRoleARN), opts.shutdownTimeout)
	if err := serveUntilShutdown(ctx, srv, listener, opts.shutdownTimeout); err != nil {
		log.Fatalf("server: %v", err)
	}
	log.Printf("bye")
}

// parseAllowedARNs parses comma-separated role identifiers (roleArn values).
// Empty entries are ignored; an empty set disables the optional local allowlist.
func parseAllowedARNs(s string) map[string]struct{} {
	m := map[string]struct{}{}
	for _, part := range strings.Split(s, ",") {
		if arn := strings.TrimSpace(part); arn != "" {
			m[arn] = struct{}{}
		}
	}
	return m
}
