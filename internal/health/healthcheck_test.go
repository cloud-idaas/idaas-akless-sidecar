package health

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// splitHostPort 从 httptest server 的 URL 里取出 host:port。
func splitHostPort(t *testing.T, u string) string {
	t.Helper()
	host, port, err := net.SplitHostPort(strings.TrimPrefix(u, "http://"))
	if err != nil {
		t.Fatalf("bad test server URL %q: %v", u, err)
	}
	return net.JoinHostPort(host, port)
}

// TestRun 覆盖 --healthcheck 的判定矩阵：就绪 / 非 200 / body 不符 /
// 端口没人监听 / 地址格式非法 / 本地健康响应超时 / 通配地址归一到 loopback。
// 断言"探到什么状态"而非"怎么探"，保证 exec 探针的退出码语义稳定。
func TestRun(t *testing.T) {
	const probeTimeout = 2 * time.Second

	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer okSrv.Close()

	badStatusSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("busy"))
	}))
	defer badStatusSrv.Close()

	badBodySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pong"))
	}))
	defer badBodySrv.Close()

	// 连接失败用例：假定本机 loopback 的端口 1 未被占用。
	const deadAddr = "127.0.0.1:1"

	slowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(1500 * time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	}))
	defer slowSrv.Close()

	tests := []struct {
		name       string
		addr       string
		timeout    time.Duration
		wantErr    bool
		wantErrSub string
	}{
		{"200 + ok → 就绪", splitHostPort(t, okSrv.URL), probeTimeout, false, ""},
		{"0.0.0.0 通配 → 归一 loopback 后仍就绪", "0.0.0.0:" + portOf(t, okSrv.URL), probeTimeout, false, ""},
		{"503 → 报错含状态码", splitHostPort(t, badStatusSrv.URL), probeTimeout, true, "HTTP 503"},
		{"200 但 body 非 ok → 报错", splitHostPort(t, badBodySrv.URL), probeTimeout, true, "unexpected body"},
		{"端口无监听 → 报错含 URL", deadAddr, probeTimeout, true, "get http://127.0.0.1:1/healthz"},
		{"无端口地址 → 格式报错", "127.0.0.1", probeTimeout, true, "invalid listen address"},
		{"上游超时 → 报错", splitHostPort(t, slowSrv.URL), 300 * time.Millisecond, true, "get http://"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Run(tt.addr, tt.timeout)
			switch {
			case tt.wantErr && err == nil:
				t.Fatalf("want error containing %q, got nil", tt.wantErrSub)
			case !tt.wantErr && err != nil:
				t.Fatalf("want nil, got %v", err)
			case tt.wantErr && !strings.Contains(err.Error(), tt.wantErrSub):
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErrSub)
			}
			if err != nil {
				t.Logf("err（会进 Pod event 的原文）: %v", err)
			}
		})
	}
}

// portOf 取 httptest URL 里的端口。
func portOf(t *testing.T, u string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(u, "http://"))
	if err != nil {
		t.Fatalf("bad test server URL %q: %v", u, err)
	}
	return port
}

// TestHealthcheckTimeoutBelowProbeDeadline 钉住一个隐性契约：自检内部超时
// （HealthcheckTimeout）必须严格小于 deployment.yaml 里 exec 探针的
// timeoutSeconds（4s），否则 kubelet 先杀进程、stderr 里的失败原因就丢了。
func TestHealthcheckTimeoutBelowProbeDeadline(t *testing.T) {
	const kubeProbeDeadline = 4 * time.Second
	if HealthcheckTimeout >= kubeProbeDeadline {
		t.Fatalf("HealthcheckTimeout=%v must be < kubelet exec probe timeoutSeconds=%v",
			fmt.Sprint(HealthcheckTimeout), fmt.Sprint(kubeProbeDeadline))
	}
}
