package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/cloud-idaas/idaas-go-core-sdk/factory"
)

func TestShutdownConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		env  string
		want time.Duration
		bad  bool
	}{
		{name: "default covers response writes", want: 15 * time.Second},
		{name: "follows longer refresh", args: []string{"--credential-refresh-timeout=30s"}, want: 35 * time.Second},
		{name: "explicit larger budget", args: []string{"--shutdown-timeout=45s"}, want: 45 * time.Second},
		{name: "environment", env: "20s", want: 20 * time.Second},
		{name: "CLI overrides invalid environment", args: []string{"--shutdown-timeout=16s"}, env: "invalid", want: 16 * time.Second},
		{name: "auto overrides environment", args: []string{"--shutdown-timeout=auto"}, env: "20s", want: 15 * time.Second},
		{name: "exact minimum", args: []string{"--shutdown-timeout=15s"}, want: 15 * time.Second},
		{name: "shorter budget rejected", args: []string{"--shutdown-timeout=14s"}, bad: true},
		{name: "zero", args: []string{"--shutdown-timeout=0s"}, bad: true},
		{name: "negative", args: []string{"--shutdown-timeout=-1s"}, bad: true},
		{name: "invalid", env: "invalid", bad: true},
		{name: "unit required", env: "15", bad: true},
		{name: "explicit empty", args: []string{"--shutdown-timeout="}, bad: true},
		{name: "empty environment uses auto", want: 15 * time.Second},
		{name: "health ignores drain policy", args: []string{"--healthcheck"}, env: "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, err := parseOptions(tc.args, func(key string) string {
				if key == "IDAAS_AKLESS_SHUTDOWN_TIMEOUT" {
					return tc.env
				}
				return ""
			}, io.Discard)
			if (err != nil) != tc.bad || err == nil && o.shutdownTimeout != tc.want {
				t.Fatalf("shutdown=%s err=%v, want=%s bad=%v", o.shutdownTimeout, err, tc.want, tc.bad)
			}
		})
	}
}

func TestVersionDoesNotInitializeSDKOrValidateRuntimeSettings(t *testing.T) {
	t.Setenv("CLOUD_IDAAS_CONFIG_PATH", "/missing/version-config.json")
	t.Setenv("IDAAS_AKLESS_SIDECAR_LISTEN_PORT", "invalid")
	t.Setenv("IDAAS_AKLESS_SHUTDOWN_TIMEOUT", "invalid")
	oldArgs, oldOutput := os.Args, os.Stdout
	oldVersion, oldRevision := version, revision
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Args, os.Stdout = oldArgs, oldOutput
		version, revision = oldVersion, oldRevision
		_ = r.Close()
		_ = w.Close()
	})
	os.Args, os.Stdout = []string{"sidecar", "--version"}, w
	version, revision = "v1.2.3", "fixture-revision"
	main()
	_ = w.Close()
	output, err := io.ReadAll(r)
	if err != nil || string(output) != "idaas-akless-sidecar version=v1.2.3 revision=fixture-revision\n" {
		t.Fatalf("version output=%q err=%v", output, err)
	}
}

func TestShutdownDrainsInFlightRequest(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	srv := newHTTPServer("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
			_, _ = io.WriteString(w, "complete")
		case <-r.Context().Done():
		}
	}), time.Second)
	defer func() { _ = srv.Close() }()
	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopping := make(chan struct{})
	srv.RegisterOnShutdown(func() { close(stopping) })
	done := make(chan error, 1)
	go func() { done <- serveUntilShutdown(ctx, srv, listener, time.Second) }()
	response := make(chan string, 1)
	go func() {
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get("http://" + listener.Addr().String())
		if err != nil {
			response <- err.Error()
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		response <- string(body)
	}()
	waitLifecycleEvent(t, started)
	cancel()
	waitLifecycleEvent(t, stopping)
	select {
	case err := <-done:
		t.Fatalf("exited before request completed: %v", err)
	default:
	}
	unblock()
	if body := <-response; body != "complete" {
		t.Fatalf("in-flight response=%q", body)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second); err == nil {
		_ = conn.Close()
		t.Fatal("listener accepted a request after shutdown")
	}
}

func TestShutdownDeadlineClosesActiveRequest(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	srv := newHTTPServer("127.0.0.1:0", http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(canceled)
	}), time.Second)
	defer func() { _ = srv.Close() }()
	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveUntilShutdown(ctx, srv, listener, 30*time.Millisecond) }()
	clientDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get("http://" + listener.Addr().String())
		if resp != nil {
			_ = resp.Body.Close()
		}
		clientDone <- err
	}()
	waitLifecycleEvent(t, started)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("want drain deadline failure, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("drain did not respect its deadline")
	}
	waitLifecycleEvent(t, canceled)
	if err := <-clientDone; err == nil {
		t.Fatal("timed-out request unexpectedly succeeded")
	}
}

func TestServeFailureAndAlreadyClosedServer(t *testing.T) {
	for _, closedServer := range []bool{false, true} {
		srv := newHTTPServer("127.0.0.1:0", http.NotFoundHandler(), time.Second)
		listener, err := net.Listen("tcp", srv.Addr)
		if err != nil {
			t.Fatal(err)
		}
		if closedServer {
			_ = srv.Close()
		} else {
			_ = listener.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err = serveUntilShutdown(ctx, srv, listener, time.Second)
		cancel()
		if closedServer && err != nil || !closedServer && !errors.Is(err, net.ErrClosed) {
			t.Fatalf("closedServer=%v, error=%v", closedServer, err)
		}
	}
}

func waitLifecycleEvent(t *testing.T, event <-chan struct{}) {
	t.Helper()
	select {
	case <-event:
	case <-time.After(3 * time.Second):
		t.Fatal("lifecycle event did not occur")
	}
}

// The child uses the production serve loop and real SIGTERM, with a fake local
// handler so no token, IDaaS configuration or cloud call is needed.
func TestShutdownSIGTERM(t *testing.T) {
	if os.Getenv("AKLESS_SHUTDOWN_CHILD") == "1" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
		defer stop()
		release := make(chan struct{})
		go func() {
			_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
			close(release)
		}()
		srv := newHTTPServer("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Println("STARTED")
			<-release
			_, _ = io.WriteString(w, "complete")
		}), time.Second)
		srv.RegisterOnShutdown(func() { fmt.Println("DRAINING") })
		listener, err := net.Listen("tcp", srv.Addr)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("READY " + listener.Addr().String())
		if err := serveUntilShutdown(ctx, srv, listener, 3*time.Second); err != nil {
			t.Fatal(err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestShutdownSIGTERM$")
	command.Env = append(os.Environ(), "AKLESS_SHUTDOWN_CHILD=1")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || !strings.HasPrefix(scanner.Text(), "READY ") {
		t.Fatal("child failed to start local listener")
	}
	url := "http://" + strings.TrimPrefix(scanner.Text(), "READY ")
	response := make(chan string, 1)
	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			response <- err.Error()
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		response <- string(body)
	}()
	if !scanner.Scan() || scanner.Text() != "STARTED" {
		t.Fatal("child request did not start")
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if !scanner.Scan() || scanner.Text() != "DRAINING" {
		t.Fatal("SIGTERM did not start the drain")
	}
	if _, err := io.WriteString(stdin, "release\n"); err != nil {
		t.Fatal(err)
	}
	if body := <-response; body != "complete" {
		t.Fatalf("SIGTERM interrupted in-flight request: %q", body)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("child exited with %v: %s", err, stderr.String())
	}
}

func TestMainStartsAndStopsWithOfflineBootstrap(t *testing.T) {
	// Guard the test process's default signal disposition even if main's signal
	// registration regresses; the completion assertion still requires main to drain.
	_, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	factory.Reset()
	t.Cleanup(factory.Reset)
	root := t.TempDir()
	configuration := map[string]any{
		"idaasInstanceId": "fixture", "clientId": "fixture",
		"issuer": "https://example.invalid", "tokenEndpoint": "https://example.invalid/token",
		"developerApiEndpoint": "https://example.invalid",
		"scope":                "urn:cloud:idaas:pam|cloud_account_role:obtain_access_credential",
		"authnConfiguration": map[string]any{
			"identityType": "CLIENT", "authnMethod": "OIDC", "clientDeployEnvironment": "KUBERNETES",
			"applicationFederatedCredentialName": "fixture", "oidcTokenFilePath": filepath.Join(root, "missing-token"),
		},
		"httpConfiguration": map[string]int{"connectTimeout": 5000, "readTimeout": 10000},
	}
	data, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "client-config.json")
	if err := os.WriteFile(config, data, 0600); err != nil {
		t.Fatal(err)
	}
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := probe.Addr().String()
	port := fmt.Sprint(probe.Addr().(*net.TCPAddr).Port)
	_ = probe.Close()
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"sidecar", "--credential-auth-enabled=false", "--config=" + config,
		"--listen-port=" + port, "--credential-refresh-timeout=1s",
		"--credential-refresh-min-validity=1m", "--shutdown-timeout=auto",
		"--default-role-arn=", "--allowed-role-arns="}
	done := make(chan struct{})
	go func() { main(); close(done) }()
	client := &http.Client{Timeout: 100 * time.Millisecond}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := client.Get("http://" + address + "/healthz")
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr != nil || resp.StatusCode != 200 || string(body) != "ok" {
				t.Fatalf("health status=%d body=%q err=%v", resp.StatusCode, body, readErr)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("main did not start its local listener")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitLifecycleEvent(t, done)
}
