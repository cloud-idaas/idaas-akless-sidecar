package credentials

// Handler tests cover query validation, role restrictions and cache admission.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloud-idaas/idaas-akless-sidecar/internal/projectedauth"
)

type testRequestAuthenticator struct{}

func (testRequestAuthenticator) Authenticate(r *http.Request) (func() error, error) {
	if r.Header.Get("Authorization") != "test-token" {
		return nil, projectedauth.ErrUnauthorized
	}
	return func() error { return nil }, nil
}

// Upstream failure distinguishes accepted requests (502) from rejected requests.
func newHandlerTestService() *Service {
	clk := &fakeClock{now: time.Now()}
	fetch := &fakeFetch{err: errors.New("test: fetch disabled")}
	s := &Service{clock: clk}
	s.fetchCredentials = fetch.fn
	return s
}

func do(t *testing.T, h http.HandlerFunc, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("Authorization", "test-token")
	rr := httptest.NewRecorder()
	h(rr, req)
	return rr
}

func TestCredentialsHandlerGates(t *testing.T) {
	const validARN = "acs:ram::123:role/test"
	const otherValid = "arn:aws:iam::456:role/other"
	allow := map[string]struct{}{validARN: {}}

	cases := []struct {
		name       string
		allowed    map[string]struct{}
		method     string
		target     string
		wantStatus int
	}{
		{"non-GET → 405", nil, http.MethodPost, "/credentials?roleArn=" + validARN, http.StatusMethodNotAllowed},
		{"missing roleArn → 400", nil, http.MethodGet, "/credentials", http.StatusBadRequest},
		{"invalid identifier → 400", nil, http.MethodGet, "/credentials?roleArn=invalid%20id", http.StatusBadRequest},
		{"not in allowlist → 403", allow, http.MethodGet, "/credentials?roleArn=" + otherValid, http.StatusForbidden},
		{"in allowlist → passes gate (reaches fetch → 502)", allow, http.MethodGet, "/credentials?roleArn=" + validARN, http.StatusBadGateway},
		{"no allowlist + valid → passes gate (reaches fetch → 502)", nil, http.MethodGet, "/credentials?roleArn=" + validARN, http.StatusBadGateway},
		{"empty allowlist + other role → server authorization", map[string]struct{}{}, http.MethodGet, "/credentials?roleArn=" + otherValid, http.StatusBadGateway},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newHandlerTestService()
			h := s.CredentialsHandler("", c.allowed, testRequestAuthenticator{})
			rr := do(t, h, c.method, c.target)
			if rr.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d (body=%q)", rr.Code, c.wantStatus, rr.Body.String())
			}
		})
	}
}

func TestCredentialsHandlerDefaultRoleOptionalAllowlist(t *testing.T) {
	const defaultARN = "acs:ram::123:role/default"
	for _, tc := range []struct {
		name    string
		allowed map[string]struct{}
		status  int
	}{
		{"omitted relies on server authorization", nil, http.StatusBadGateway},
		{"empty relies on server authorization", map[string]struct{}{}, http.StatusBadGateway},
		{"configured default allowed", map[string]struct{}{defaultARN: {}}, http.StatusBadGateway},
		{"configured default denied", map[string]struct{}{"acs:ram::123:role/other": {}}, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHandlerTestService().CredentialsHandler(defaultARN, tc.allowed, testRequestAuthenticator{})
			rr := do(t, h, http.MethodGet, "/credentials")
			if rr.Code != tc.status {
				t.Fatalf("status = %d, want %d", rr.Code, tc.status)
			}
		})
	}
}

// TestCredentialsHandlerCacheCap verifies the admission limit while all retained
// failed lookups are still in backoff. Reclamation after backoff is tested in cache_test.go.
func TestCredentialsHandlerCacheCap(t *testing.T) {
	s := newHandlerTestService()
	h := s.CredentialsHandler("", nil, testRequestAuthenticator{}) // 无白名单：只靠基础格式检查 + 缓存上限收口

	// Fill capacity with failed lookups still in backoff.
	for i := 0; i < maxCredentialCacheEntries; i++ {
		arn := "acs:ram::" + itoa(i) + ":role/r"
		rr := do(t, h, http.MethodGet, "/credentials?roleArn="+arn)
		if rr.Code != http.StatusBadGateway {
			t.Fatalf("fill phase arn#%d: status=%d, want 502", i, rr.Code)
		}
	}
	// A new role must not exceed the entry limit.
	overflow := "acs:ram::999:role/overflow"
	rr := do(t, h, http.MethodGet, "/credentials?roleArn="+overflow)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("overflow: status=%d, want 502 (cache full should still surface as 502)", rr.Code)
	}
	var n int64
	s.credentialCache.Range(func(_, _ any) bool { n++; return true })
	if n > maxCredentialCacheEntries {
		t.Fatalf("cache entries = %d, exceeds cap %d", n, maxCredentialCacheEntries)
	}
}

// itoa formats the nonnegative role numbers used in this test.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
