package credentials

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloud-idaas/idaas-akless-sidecar/internal/projectedauth"
)

func realAuthenticator(t *testing.T) (*projectedauth.Authenticator, string) {
	t.Helper()
	now := time.Now()
	claims := map[string]any{"aud": []string{projectedauth.Audience}, "sub": "system:serviceaccount:ns:sa", "iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(time.Hour).Unix(), "kubernetes.io": map[string]any{"namespace": "ns", "pod": map[string]string{"uid": "pod"}, "serviceaccount": map[string]string{"name": "sa"}}}
	data, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`)) + "." + base64.RawURLEncoding.EncodeToString(data) + ".signature"
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(token), 0440); err != nil {
		t.Fatal(err)
	}
	a, err := projectedauth.New(path, projectedauth.PodContext{UID: "pod", Namespace: "ns", ServiceAccount: "sa"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, token
}

func TestSSRFCannotReadCachedCredentialsOrTriggerFetch(t *testing.T) {
	auth, token := realAuthenticator(t)
	clk := &fakeClock{now: time.Now()}
	fetch := &fakeFetch{resp: respWithExpiry(vendorAWS, clk.now.Add(time.Hour).Unix())}
	s := newTestService(clk, fetch)
	for _, suffix := range []string{"", "?format=raw"} {
		handler := s.CredentialsHandler(testRoleARN, nil, auth)
		for _, tc := range []struct {
			name, header string
			status       int
		}{
			{"missing", "", 401}, {"wrong", "a.b.c", 401}, {"Bearer", "Bearer " + token, 400}, {"valid", token, 200},
			{"missing after cache warm", "", 401}, {"wrong after cache warm", "a.b.c", 401},
		} {
			t.Run(suffix+tc.name, func(t *testing.T) {
				before := fetch.count()
				r := httptest.NewRequest(http.MethodGet, "/credentials"+suffix, nil)
				if tc.header != "" {
					r.Header.Set("Authorization", tc.header)
				}
				w := httptest.NewRecorder()
				handler(w, r)
				if w.Code != tc.status {
					t.Fatalf("status %d want %d", w.Code, tc.status)
				}
				if tc.status != 200 {
					if fetch.count() != before {
						t.Fatal("rejected request triggered fetch")
					}
					if strings.Contains(w.Body.String(), "SessionToken") || strings.Contains(w.Body.String(), token) {
						t.Fatal("secret in rejected response")
					}
				}
			})
		}
	}
	if fetch.count() != 1 {
		t.Fatalf("expected one shared cached fetch, got %d", fetch.count())
	}
}

func TestExplicitAnonymousModeSharesCacheAcrossFormats(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	fetch := &fakeFetch{resp: respWithExpiry(vendorAWS, clk.now.Add(time.Hour).Unix())}
	s := newTestService(clk, fetch)
	handler := s.CredentialsHandler(testRoleARN, nil, UnauthenticatedAccess{})
	for _, suffix := range []string{"", "?format=raw", "", "?format=raw"} {
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequest(http.MethodGet, "/credentials"+suffix, nil))
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("anonymous response: status=%d body=%s", response.Code, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), "fake-id") {
			t.Fatal("anonymous response omitted credentials")
		}
	}
	if fetch.count() != 1 {
		t.Fatalf("anonymous formats did not share cache: %d fetches", fetch.count())
	}
}

func TestExplicitAnonymousModeRetainsRequestAndRoleGates(t *testing.T) {
	for _, tc := range []struct {
		method, target string
		status         int
	}{
		{http.MethodPost, "/credentials", http.StatusMethodNotAllowed},
		{http.MethodGet, "/credentials?roleArn=other-role", http.StatusForbidden},
		{http.MethodGet, "/credentials?roleArn=other-role&format=raw", http.StatusForbidden},
		{http.MethodGet, "/credentials?format=invalid", http.StatusBadRequest},
		{http.MethodGet, "/credentials?roleArn=a&roleArn=b", http.StatusBadRequest},
	} {
		clk := &fakeClock{now: time.Now()}
		fetch := &fakeFetch{resp: respWithExpiry(vendorAWS, clk.now.Add(time.Hour).Unix())}
		s := newTestService(clk, fetch)
		handler := s.CredentialsHandler(testRoleARN, map[string]struct{}{testRoleARN: {}}, UnauthenticatedAccess{})
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequest(tc.method, tc.target, nil))
		if response.Code != tc.status || fetch.count() != 0 {
			t.Fatalf("%s %s: status=%d fetches=%d", tc.method, tc.target, response.Code, fetch.count())
		}
	}
}

func TestExplicitAnonymousModeRejectsExpiredCredentials(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	fetch := &fakeFetch{resp: respWithExpiry(vendorAWS, clk.now.Add(-time.Second).Unix())}
	s := newTestService(clk, fetch)
	handler := s.CredentialsHandler(testRoleARN, nil, UnauthenticatedAccess{})
	for _, suffix := range []string{"", "?format=raw"} {
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequest(http.MethodGet, "/credentials"+suffix, nil))
		if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "fake-secret") {
			t.Fatalf("expired anonymous credential leaked: status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if fetch.count() != 1 {
		t.Fatalf("anonymous failure bypassed backoff: %d fetches", fetch.count())
	}
}

type finalCheckAuthenticator struct{ final error }

func (a finalCheckAuthenticator) Authenticate(*http.Request) (func() error, error) {
	return func() error { return a.final }, nil
}

func TestAuthenticationRecheckedBeforeOutput(t *testing.T) {
	_, diagnostic := projectedauth.New(filepath.Join(t.TempDir(), "missing-token"), projectedauth.PodContext{UID: "pod", Namespace: "ns", ServiceAccount: "sa"})
	if !errors.Is(diagnostic, projectedauth.ErrUnavailable) {
		t.Fatalf("missing reference accepted: %v", diagnostic)
	}
	for _, tc := range []struct {
		name string
		auth RequestAuthenticator
		want int
	}{
		{"missing authenticator", nil, 503},
		{"expired during fetch", finalCheckAuthenticator{projectedauth.ErrUnauthorized}, 401},
		{"file lost during fetch", finalCheckAuthenticator{projectedauth.ErrUnavailable}, 503},
		{"internal diagnostic stays private", finalCheckAuthenticator{diagnostic}, 503},
		{"unexpected failure", finalCheckAuthenticator{errors.New("secret test error")}, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clk := &fakeClock{now: time.Now()}
			s := newTestService(clk, &fakeFetch{resp: respWithExpiry(vendorAWS, clk.now.Add(time.Hour).Unix())})
			w := do(t, s.CredentialsHandler(testRoleARN, nil, tc.auth), http.MethodGet, "/credentials")
			if w.Code != tc.want || strings.Contains(w.Body.String(), "secret test error") || strings.Contains(w.Body.String(), "reason=") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
		})
	}
}

func TestCredentialAuthenticationOverHTTP(t *testing.T) {
	auth, token := realAuthenticator(t)
	clk := &fakeClock{now: time.Now()}
	fetch := &fakeFetch{resp: respWithExpiry(vendorAWS, clk.now.Add(time.Hour).Unix())}
	s := newTestService(clk, fetch)
	mux := http.NewServeMux()
	mux.HandleFunc("/credentials", s.CredentialsHandler(testRoleARN, nil, auth))
	server := httptest.NewServer(mux)
	defer server.Close()
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	for _, tc := range []struct {
		suffix string
		values []string
		want   int
	}{
		{"", nil, 401}, {"?format=raw", nil, 401}, {"", []string{token}, 200},
		{"?format=raw", nil, 401}, {"", []string{token, token}, 400},
		{"?format=raw", []string{token}, 200},
	} {
		req, err := http.NewRequest(http.MethodGet, server.URL+"/credentials"+tc.suffix, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range tc.values {
			req.Header.Add("Authorization", value)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != tc.want {
			t.Fatalf("status %d want %d", resp.StatusCode, tc.want)
		}
	}
	if fetch.count() != 1 {
		t.Fatalf("unexpected upstream calls: %d", fetch.count())
	}
}

type incompleteAuthenticator struct{}

func (incompleteAuthenticator) Authenticate(*http.Request) (func() error, error) { return nil, nil }

func TestIncompleteAuthenticatorCannotTriggerFetch(t *testing.T) {
	s := newHandlerTestService()
	w := do(t, s.CredentialsHandler(testRoleARN, nil, incompleteAuthenticator{}), http.MethodGet, "/credentials")
	if w.Code != 503 {
		t.Fatalf("status=%d", w.Code)
	}
	if _, ok := s.credentialCache.Load(testRoleARN); ok {
		t.Fatal("invalid authentication reached cache")
	}
}
