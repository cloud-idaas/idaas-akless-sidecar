package credentials

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDeveloperAPIRedirectNeverForwardsCredential(t *testing.T) {
	for _, code := range []int{301, 302, 303, 307, 308} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			forwarded := false
			target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				forwarded = true
				_, _ = w.Write([]byte(`{}`))
			}))
			defer target.Close()
			source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fake-bootstrap-token" {
					t.Error("bootstrap auth missing")
				}
				http.Redirect(w, r, target.URL, code)
			}))
			defer source.Close()
			client := newDeveloperAPIClient(time.Second)
			defer client.CloseIdleConnections()
			client.Transport.(*http.Transport).TLSClientConfig.RootCAs = source.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
			s := &Service{provider: fakeProvider{}, httpClient: client, developerAPIEndpoint: source.URL, idaasInstanceID: "test"}
			_, err := s.callDeveloperAPI(context.Background(), "role")
			if err == nil || forwarded {
				t.Fatal("redirect was followed or accepted")
			}
		})
	}
}

func TestDeveloperAPIDoesNotUseEnvironmentProxy(t *testing.T) {
	proxyCalled := false
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalled = true
		w.WriteHeader(502)
	}))
	defer proxy.Close()
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(name, proxy.URL)
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer upstream.Close()
	client := newDeveloperAPIClient(time.Second)
	defer client.CloseIdleConnections()
	transport := client.Transport.(*http.Transport)
	transport.TLSClientConfig.RootCAs = upstream.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	transport.TLSClientConfig.ServerName = upstream.Certificate().DNSNames[0]
	transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", upstream.Listener.Addr().String())
	}
	s := &Service{provider: fakeProvider{}, httpClient: client, developerAPIEndpoint: "https://upstream.example.invalid", idaasInstanceID: "test"}
	if _, err := s.callDeveloperAPI(context.Background(), "role"); err != nil || proxyCalled {
		t.Fatalf("direct request failed: %v", err)
	}
	if transport.Proxy != nil || client.Timeout != time.Second {
		t.Fatal("unsafe client defaults")
	}
}

func TestDeveloperAPIResponseLimitAndErrorBody(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status, size int
		wantError    bool
	}{
		{"exact limit", 200, 1024 * 1024, false},
		{"exceeds limit", 200, 1024*1024 + 1, true},
		{"error body not read", 403, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body io.ReadCloser = io.NopCloser(strings.NewReader(`{}` + strings.Repeat(" ", max(0, tc.size-2))))
			if tc.status != 200 {
				body = failingBody{}
			}
			s := &Service{provider: fakeProvider{}, developerAPIEndpoint: "https://example.invalid", idaasInstanceID: "test", httpClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: body}, nil
			})}}
			_, err := s.callDeveloperAPI(context.Background(), "role")
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v", err)
			}
			if tc.status != 200 && !strings.Contains(err.Error(), "status 403") {
				t.Fatal("error response body read")
			}
		})
	}
}

func TestDeveloperAPIRejectsUnsafeEndpointBeforeSending(t *testing.T) {
	for _, endpoint := range []string{"http://example.invalid", "https://user:secret@example.invalid", "https://example.invalid/#fragment"} {
		s := &Service{provider: fakeProvider{}, developerAPIEndpoint: endpoint, idaasInstanceID: "test", httpClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("unsafe endpoint was requested")
			return nil, nil
		})}}
		if _, err := s.callDeveloperAPI(context.Background(), "role"); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("unsafe endpoint accepted or leaked")
		}
	}
}
