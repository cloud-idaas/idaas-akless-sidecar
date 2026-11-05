package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cloud-idaas/idaas-akless-sidecar/internal/projectedauth"
)

func TestParseAllowedARNsOptional(t *testing.T) {
	const arn = "acs:ram::123:role/test"
	const other = "arn:aws:iam::456:role/other"
	for _, tc := range []struct {
		name, value string
		want        map[string]struct{}
	}{
		{"omitted disables local allowlist", "", map[string]struct{}{}},
		{"whitespace disables local allowlist", " , ", map[string]struct{}{}},
		{"bare ARN", arn, map[string]struct{}{arn: {}}},
		{"multiple roles and duplicates", " " + arn + ", " + other + ", " + arn, map[string]struct{}{arn: {}, other: {}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseAllowedARNs(tc.value); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestConfigurationContract(t *testing.T) {
	const arn = "acs:ram::123:role/test"
	for _, tc := range []struct {
		name      string
		args      []string
		env       map[string]string
		check     func(*testing.T, options)
		wantError bool
	}{
		{name: "defaults", check: func(t *testing.T, o options) {
			if o.configPath != "/etc/idaas-akless/client-config.json" || o.listenAddr != "127.0.0.1:61765" || o.refreshTimeout != 10*time.Second || o.refreshMinValidity != 5*time.Minute || o.allowedRoleARNs != "" || o.authTokenFile != projectedauth.DefaultTokenFile || !o.authEnabled {
				t.Fatalf("unexpected defaults: %+v", o)
			}
		}},
		{name: "environment", env: map[string]string{"CLOUD_IDAAS_CONFIG_PATH": "/tmp/config.json", "IDAAS_AKLESS_SIDECAR_LISTEN_PORT": "9090", "IDAAS_AKLESS_DEFAULT_ROLE_ARN": arn, "IDAAS_AKLESS_ALLOWED_ROLE_ARNS": arn, "IDAAS_AKLESS_CREDENTIAL_REFRESH_TIMEOUT": "20s"}, check: func(t *testing.T, o options) {
			if o.configPath != "/tmp/config.json" || o.listenAddr != "127.0.0.1:9090" || o.defaultRoleARN != arn || o.allowedRoleARNs != arn || o.refreshTimeout != 20*time.Second {
				t.Fatalf("environment not applied: %+v", o)
			}
		}},
		{name: "explicit CLI overrides environment including empty allowlist", args: []string{"--config=/cli.json", "--listen-port=8081", "--default-role-arn=cli", "--allowed-role-arns=", "--credential-refresh-timeout=3s"}, env: map[string]string{"CLOUD_IDAAS_CONFIG_PATH": "/env.json", "IDAAS_AKLESS_DEFAULT_ROLE_ARN": arn, "IDAAS_AKLESS_ALLOWED_ROLE_ARNS": arn, "IDAAS_AKLESS_CREDENTIAL_REFRESH_TIMEOUT": "invalid"}, check: func(t *testing.T, o options) {
			if o.configPath != "/cli.json" || o.listenAddr != "127.0.0.1:8081" || o.defaultRoleARN != "cli" || o.allowedRoleARNs != "" || o.refreshTimeout != 3*time.Second {
				t.Fatalf("CLI precedence broken: %+v", o)
			}
		}},
		{name: "duration requires unit", args: []string{"--credential-refresh-timeout=10"}, wantError: true},
		{name: "duration cannot be zero", args: []string{"--credential-refresh-timeout=0s"}, wantError: true},
		{name: "duration cannot be negative", args: []string{"--credential-refresh-timeout=-1s"}, wantError: true},
		{name: "empty config path", args: []string{"--config="}, wantError: true},
		{name: "empty auth path", args: []string{"--credential-auth-token-file="}, wantError: true},
		{name: "relative auth path", args: []string{"--credential-auth-token-file=token"}, wantError: true},
		{name: "empty auth path environment uses default", env: map[string]string{"IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE": ""}, check: func(t *testing.T, o options) {
			if o.authTokenFile != projectedauth.DefaultTokenFile {
				t.Fatal(o.authTokenFile)
			}
		}},
		{name: "auth path environment", env: map[string]string{"IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE": "/run/custom/token"}, check: func(t *testing.T, o options) {
			if o.authTokenFile != "/run/custom/token" {
				t.Fatal(o.authTokenFile)
			}
		}},
		{name: "auth path CLI precedence", args: []string{"--credential-auth-token-file=/run/cli/token"}, env: map[string]string{"IDAAS_AKLESS_CREDENTIAL_AUTH_TOKEN_FILE": "/run/env/token"}, check: func(t *testing.T, o options) {
			if o.authTokenFile != "/run/cli/token" {
				t.Fatal(o.authTokenFile)
			}
		}},
		{name: "unknown flag", args: []string{"--unknown=value"}, wantError: true},
		{name: "positional arguments", args: []string{"unexpected"}, wantError: true},
		{name: "healthcheck ignores upstream config", args: []string{"--healthcheck"}, env: map[string]string{"IDAAS_AKLESS_CREDENTIAL_REFRESH_TIMEOUT": "invalid"}, check: func(t *testing.T, o options) {
			if !o.healthcheck {
				t.Fatal("not in health mode")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseOptions(tc.args, func(key string) string { return tc.env[key] }, io.Discard)
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v, wantError=%v", err, tc.wantError)
			}
			if err == nil && tc.check != nil {
				tc.check(t, got)
			}
		})
	}
}

func TestCredentialRefreshMinimumValidityConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		args      []string
		want      time.Duration
		wantError bool
	}{
		{name: "default", want: 5 * time.Minute},
		{name: "environment", env: "12m", want: 12 * time.Minute},
		{name: "CLI", args: []string{"--credential-refresh-min-validity=90s"}, want: 90 * time.Second},
		{name: "CLI overrides invalid environment", env: "invalid", args: []string{"--credential-refresh-min-validity=2m"}, want: 2 * time.Minute},
		{name: "invalid environment", env: "invalid", wantError: true},
		{name: "unit required", args: []string{"--credential-refresh-min-validity=60"}, wantError: true},
		{name: "zero", args: []string{"--credential-refresh-min-validity=0s"}, wantError: true},
		{name: "negative", args: []string{"--credential-refresh-min-validity=-1s"}, wantError: true},
		{name: "health probe ignores refresh policy", env: "invalid", args: []string{"--healthcheck"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, err := parseOptions(tc.args, func(key string) string {
				if key == "IDAAS_AKLESS_CREDENTIAL_REFRESH_MIN_VALIDITY" {
					return tc.env
				}
				return ""
			}, io.Discard)
			if (err != nil) != tc.wantError || err == nil && o.refreshMinValidity != tc.want {
				t.Fatalf("minimum validity=%s err=%v, want=%s error=%v", o.refreshMinValidity, err, tc.want, tc.wantError)
			}
		})
	}
}

func TestCredentialAuthConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, environment string
		args              []string
		want              bool
		wantError         bool
	}{
		{name: "default enabled", want: true},
		{name: "environment enabled", environment: "true", want: true},
		{name: "environment disabled", environment: "false"},
		{name: "CLI enables over environment", environment: "false", args: []string{"--credential-auth-enabled=true"}, want: true},
		{name: "CLI disables over invalid environment", environment: "invalid", args: []string{"--credential-auth-enabled=false"}},
		{name: "disabled ignores unused token path", args: []string{"--credential-auth-enabled=false", "--credential-auth-token-file="}},
		{name: "invalid environment", environment: "invalid", wantError: true},
		{name: "numeric false rejected", environment: "0", wantError: true},
		{name: "invalid CLI", args: []string{"--credential-auth-enabled=invalid"}, wantError: true},
		{name: "empty CLI", args: []string{"--credential-auth-enabled="}, wantError: true},
		{name: "enabled still requires token path", args: []string{"--credential-auth-enabled=true", "--credential-auth-token-file="}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, err := parseOptions(tc.args, func(key string) string {
				if key == "IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED" {
					return tc.environment
				}
				return ""
			}, io.Discard)
			if (err != nil) != tc.wantError || err == nil && o.authEnabled != tc.want {
				t.Fatalf("enabled=%v err=%v; want enabled=%v error=%v", o.authEnabled, err, tc.want, tc.wantError)
			}
		})
	}
}

// Exercise the real startup selection without contacting IDaaS: both config and
// local token paths are absent, so the failure stage reveals the selected mode.
func TestCredentialAuthStartup(t *testing.T) {
	if directory := os.Getenv("AKLESS_AUTH_STARTUP_CHILD"); directory != "" {
		os.Args = []string{"sidecar", "--config=" + filepath.Join(directory, "missing-config.json"),
			"--credential-auth-token-file=" + filepath.Join(directory, "missing-token")}
		if mode := os.Getenv("AKLESS_AUTH_TEST_MODE"); mode != "default" {
			os.Args = append(os.Args, "--credential-auth-enabled="+mode)
		}
		main()
		return
	}
	for _, mode := range []string{"default", "true", "false"} {
		t.Run(mode, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestCredentialAuthStartup$")
			command.Env = append(os.Environ(), "AKLESS_AUTH_STARTUP_CHILD="+t.TempDir(),
				"AKLESS_AUTH_TEST_MODE="+mode, "IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED=true")
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatal("startup accepted absent IDaaS configuration")
			}
			if mode == "false" {
				if !strings.Contains(string(output), "WARN local credential authentication disabled") ||
					!strings.Contains(string(output), "initialize IDaaS Core SDK") {
					t.Fatalf("disabled mode did not retain upstream authentication: %s", output)
				}
			} else if !strings.Contains(string(output), "initialize local credential authentication failed") ||
				strings.Contains(string(output), "authentication disabled") || !strings.Contains(string(output), "reason=") {
				t.Fatalf("enabled mode failed to reject an absent local token: %s", output)
			}
		})
	}
}

func TestListenPortConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, current, want string
		args                []string
		wantError           bool
	}{
		{name: "default", want: "127.0.0.1:61765"},
		{name: "legacy port via environment", current: "8080", want: "127.0.0.1:8080"},
		{name: "legacy port via CLI", args: []string{"--listen-port=8080"}, want: "127.0.0.1:8080"},
		{name: "environment", current: "8081", want: "127.0.0.1:8081"},
		{name: "CLI overrides environment", current: "8081", args: []string{"--listen-port=8083"}, want: "127.0.0.1:8083"},
		{name: "CLI overrides invalid environment", current: "invalid", args: []string{"--listen-port=8083"}, want: "127.0.0.1:8083"},
		{name: "minimum", args: []string{"--listen-port=1"}, want: "127.0.0.1:1"},
		{name: "maximum", args: []string{"--listen-port=65535"}, want: "127.0.0.1:65535"},
		{name: "invalid environment", current: "invalid", wantError: true},
		{name: "zero", args: []string{"--listen-port=0"}, wantError: true},
		{name: "negative", args: []string{"--listen-port=-1"}, wantError: true},
		{name: "too large", args: []string{"--listen-port=65536"}, wantError: true},
		{name: "overflow", args: []string{"--listen-port=999999999999999999999999"}, wantError: true},
		{name: "empty", args: []string{"--listen-port="}, wantError: true},
		{name: "hostname", args: []string{"--listen-port=localhost:61765"}, wantError: true},
		{name: "wildcard", args: []string{"--listen-port=0.0.0.0:61765"}, wantError: true},
		{name: "IPv6", args: []string{"--listen-port=[::1]:61765"}, wantError: true},
		{name: "removed listen flag", args: []string{"--listen=127.0.0.1:61765"}, wantError: true},
	} {
		for _, health := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/health=%v", tc.name, health), func(t *testing.T) {
				env := map[string]string{
					"IDAAS_AKLESS_SIDECAR_LISTEN_PORT": tc.current,
					// The removed address variable cannot change the fixed host or port.
					"IDAAS_AKLESS_SIDECAR_LISTEN_ADDR": "0.0.0.0:9999",
				}
				args := append([]string(nil), tc.args...)
				if health {
					args = append(args, "--healthcheck")
				}
				got, err := parseOptions(args, func(key string) string { return env[key] }, io.Discard)
				if (err != nil) != tc.wantError || err == nil && got.listenAddr != tc.want {
					t.Fatalf("address=%q, error=%v, want=%q", got.listenAddr, err, tc.want)
				}
			})
		}
	}
}

// Exercise the actual healthcheck entry point without initializing IDaaS.
func TestHealthcheckPortStartup(t *testing.T) {
	if os.Getenv("AKLESS_HEALTH_PORT_CHILD") == "1" {
		os.Args = []string{"sidecar", "--healthcheck"}
		if port := os.Getenv("AKLESS_HEALTH_CLI_PORT"); port != "" {
			os.Args = append(os.Args, "--listen-port="+port)
		}
		main()
		return
	}
	requests := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	port := strconv.Itoa(server.Listener.Addr().(*net.TCPAddr).Port)
	for _, cli := range []bool{false, true} {
		t.Run(fmt.Sprintf("CLI=%v", cli), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHealthcheckPortStartup$")
			portEnv, portCLI := port, ""
			if cli {
				portEnv, portCLI = "invalid", port
			}
			command.Env = append(os.Environ(), "AKLESS_HEALTH_PORT_CHILD=1",
				"AKLESS_HEALTH_CLI_PORT="+portCLI, "IDAAS_AKLESS_SIDECAR_LISTEN_PORT="+portEnv,
				"IDAAS_AKLESS_SIDECAR_LISTEN_ADDR=0.0.0.0:1",
				"CLOUD_IDAAS_CONFIG_PATH=/missing/healthcheck-config.json",
				"IDAAS_AKLESS_CREDENTIAL_REFRESH_TIMEOUT=invalid")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("healthcheck failed: %v, output=%s", err, output)
			}
			select {
			case path := <-requests:
				if path != "/healthz" {
					t.Fatalf("unexpected health path %q", path)
				}
			default:
				t.Fatal("healthcheck did not contact the configured local port")
			}
		})
	}
}
