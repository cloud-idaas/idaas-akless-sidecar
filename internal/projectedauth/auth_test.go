package projectedauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var testPod = PodContext{UID: "pod-uid", Namespace: "test-ns", ServiceAccount: "test-sa"}

func claimsAt(now time.Time, nonce string) map[string]any {
	return map[string]any{
		"iss": "https://cluster.example", "sub": "system:serviceaccount:test-ns:test-sa", "aud": []string{Audience},
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(10 * time.Minute).Unix(), "nonce": nonce,
		"kubernetes.io": map[string]any{"namespace": "test-ns", "pod": map[string]string{"uid": "pod-uid"}, "serviceaccount": map[string]string{"name": "test-sa"}},
	}
}

func encodeClaims(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func publish(t *testing.T, path, token string) {
	t.Helper()
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tmp, 0440); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) (*Authenticator, string, string, *time.Time) {
	t.Helper()
	now := time.Now().Truncate(time.Second)
	path := filepath.Join(t.TempDir(), "token")
	token := encodeClaims(t, claimsAt(now, "one"))
	publish(t, path, token)
	a, err := New(path, testPod)
	if err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return now }
	t.Cleanup(func() { _ = a.Close() })
	return a, path, token, &now
}

func authenticate(a *Authenticator, token string) (func() error, error) {
	r := httptest.NewRequest("GET", "/credentials", nil)
	if token != "" {
		r.Header.Set("Authorization", token)
	}
	return a.Authenticate(r)
}

func TestHeaderAuthentication(t *testing.T) {
	a, _, token, _ := fixture(t)
	for _, tc := range []struct {
		name, token string
		want        error
	}{
		{"valid", token, nil}, {"missing", "", ErrUnauthorized},
		{"other token", "a.b.c", ErrUnauthorized}, {"bearer", "Bearer " + token, ErrInvalidRequest},
		{"comma", token + "," + token, ErrInvalidRequest}, {"newline", token + "\n", ErrInvalidRequest},
		{"oversized", strings.Repeat("a", MaxTokenBytes+1), ErrInvalidRequest},
		{"missing segment", "a..b", ErrInvalidRequest}, {"padding", "a.b.c=", ErrInvalidRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check, err := authenticate(a, tc.token)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want=%v", err, tc.want)
			}
			if err == nil && check() != nil {
				t.Fatal("valid token rejected at output")
			}
		})
	}
	r := httptest.NewRequest("GET", "/credentials", nil)
	r.Header["Authorization"] = []string{token, token}
	if _, err := a.Authenticate(r); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
	r.Header["Authorization"] = []string{token}
	r.Header["authorization"] = []string{token}
	if _, err := a.Authenticate(r); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
}

func TestReferenceClaimsFailClosed(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"wrong audience", func(c map[string]any) { c["aud"] = []string{"idaas"} }},
		{"extra audience", func(c map[string]any) { c["aud"] = []string{Audience, "api"} }},
		{"missing exp", func(c map[string]any) { delete(c, "exp") }},
		{"missing iat", func(c map[string]any) { delete(c, "iat") }},
		{"missing nbf", func(c map[string]any) { delete(c, "nbf") }},
		{"invalid date", func(c map[string]any) { c["exp"] = "invalid" }},
		{"expired", func(c map[string]any) { c["exp"] = now.Unix() }},
		{"future iat", func(c map[string]any) { c["iat"] = now.Add(time.Minute).Unix() }},
		{"future nbf", func(c map[string]any) { c["nbf"] = now.Add(time.Minute).Unix() }},
		{"wrong subject", func(c map[string]any) { c["sub"] = "system:serviceaccount:test-ns:other" }},
		{"other pod", func(c map[string]any) {
			c["kubernetes.io"].(map[string]any)["pod"] = map[string]string{"uid": "other-pod"}
		}},
		{"wrong namespace", func(c map[string]any) { c["kubernetes.io"].(map[string]any)["namespace"] = "other" }},
		{"wrong SA", func(c map[string]any) {
			c["kubernetes.io"].(map[string]any)["serviceaccount"] = map[string]string{"name": "other"}
		}},
		{"unbound", func(c map[string]any) { delete(c, "kubernetes.io") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := claimsAt(now, "bad")
			tc.edit(claims)
			path := filepath.Join(t.TempDir(), "token")
			publish(t, path, encodeClaims(t, claims))
			if a, err := New(path, testPod); !errors.Is(err, ErrUnavailable) {
				if a != nil {
					_ = a.Close()
				}
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestProjectedRotationAndFinalCheck(t *testing.T) {
	a, path, first, now := fixture(t)
	check, err := authenticate(a, first)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(10 * time.Second)
	second := encodeClaims(t, claimsAt(*now, "two"))
	publish(t, path, second)
	if _, err := authenticate(a, second); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(59 * time.Second)
	if err := check(); err != nil {
		t.Fatal("previous token lost before grace", err)
	}
	*now = now.Add(time.Second)
	if err := check(); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("grace extended", err)
	}
	if _, err := authenticate(a, second); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := authenticate(a, second); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	publish(t, path, first)
	if _, err := authenticate(a, second); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("restored historical token after failure", err)
	}
	if _, err := authenticate(a, first); err != nil {
		t.Fatal("current file did not recover", err)
	}
}

func TestDelayedObservationCannotExtendPreviousTokenGrace(t *testing.T) {
	a, path, old, now := fixture(t)
	*now = now.Add(time.Minute)
	publish(t, path, encodeClaims(t, claimsAt(*now, "new")))
	*now = now.Add(2 * time.Minute)
	if _, err := authenticate(a, old); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("late observation extended grace after new token issuance", err)
	}
}

func TestFutureIssuanceCannotExtendObservationGrace(t *testing.T) {
	a, path, old, now := fixture(t)
	future := now.Add(clockSkew)
	publish(t, path, encodeClaims(t, claimsAt(future, "future")))
	if _, err := authenticate(a, old); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(previousGrace)
	if _, err := authenticate(a, old); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("future issuance extended observation grace", err)
	}
}

func TestExpirationAndMaximumAge(t *testing.T) {
	for _, extended := range []bool{false, true} {
		t.Run(map[bool]string{false: "actual expiry", true: "one hour cap"}[extended], func(t *testing.T) {
			a, path, _, now := fixture(t)
			claims := claimsAt(*now, "expiry")
			duration := 10 * time.Minute
			if extended {
				claims["exp"] = now.Add(24 * time.Hour).Unix()
				duration = time.Hour
			}
			token := encodeClaims(t, claims)
			publish(t, path, token)
			check, err := authenticate(a, token)
			if err != nil {
				t.Fatal(err)
			}
			*now = now.Add(duration)
			if err := check(); !errors.Is(err, ErrUnavailable) {
				t.Fatal("expired reference accepted", err)
			}
		})
	}
}

func TestPreviousNeverOutlivesOwnExpiryOrTwoGenerations(t *testing.T) {
	a, path, _, now := fixture(t)
	claims := claimsAt(*now, "short")
	claims["exp"] = now.Add(20 * time.Second).Unix()
	first := encodeClaims(t, claims)
	publish(t, path, first)
	check, err := authenticate(a, first)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(10 * time.Second)
	second := encodeClaims(t, claimsAt(*now, "next"))
	publish(t, path, second)
	if _, err := authenticate(a, second); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(10 * time.Second)
	if err := check(); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("previous outlived own expiry", err)
	}
	third := encodeClaims(t, claimsAt(*now, "third"))
	publish(t, path, third)
	if _, err := authenticate(a, third); err != nil {
		t.Fatal(err)
	}
	if _, err := authenticate(a, first); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("old generation resurrected", err)
	}
}

func TestSafeTokenPermissions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mode  os.FileMode
		owner uint32
		euid  int
		want  bool
	}{
		{"readonly", 0440, 10001, 10001, true},
		{"readonly root reader", 0440, 0, 0, true},
		{"kubelet fsGroup projection", 0640, 0, 10001, true},
		{"business projection", 0640, 0, 1000, true},
		{"service writable", 0640, 10001, 10001, false},
		{"other owner writable", 0640, 1000, 10001, false},
		{"root writable reader", 0640, 0, 0, false},
		{"group writable", 0660, 0, 10001, false},
		{"other writable", 0642, 0, 10001, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := safeTokenPermissions(tc.mode, tc.owner, tc.euid); got != tc.want {
				t.Fatalf("safeTokenPermissions = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUnsafeFilesAndConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(string)
	}{
		{"missing", func(string) {}},
		{"empty", func(p string) { publish(t, p, "") }},
		{"malformed JWT", func(p string) { publish(t, p, "a.b.c") }},
		{"oversized", func(p string) { publish(t, p, strings.Repeat("a", MaxTokenBytes+1)) }},
		{"writable", func(p string) {
			publish(t, p, encodeClaims(t, claimsAt(time.Now(), "writable")))
			if err := os.Chmod(p, 0640); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory", func(p string) {
			if err := os.Mkdir(p, 0700); err != nil {
				t.Fatal(err)
			}
		}},
		{"FIFO", func(p string) {
			if err := syscall.Mkfifo(p, 0440); err != nil {
				t.Fatal(err)
			}
		}},
		{"escape", func(p string) {
			if err := os.Symlink("../outside", p); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "token")
			tc.setup(p)
			if a, err := New(p, testPod); !errors.Is(err, ErrUnavailable) {
				if a != nil {
					_ = a.Close()
				}
				t.Fatal(err)
			}
		})
	}
	for _, p := range []string{"relative/token", "/nonexistent/idaas/token"} {
		if _, err := New(p, testPod); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	}
	if _, err := New("/token", PodContext{}); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestKubeletLinksAndConcurrentRequests(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	root := t.TempDir()
	first := encodeClaims(t, claimsAt(now, "generation1"))
	second := encodeClaims(t, claimsAt(now, "generation2"))
	for _, generation := range []struct{ name, token string }{{"generation1", first}, {"generation2", second}} {
		if err := os.Mkdir(filepath.Join(root, generation.name), 0750); err != nil {
			t.Fatal(err)
		}
		publish(t, filepath.Join(root, generation.name, "token"), generation.token)
	}
	for _, link := range []struct{ target, name string }{{"generation1", "..data"}, {"..data/token", "token"}} {
		if err := os.Symlink(link.target, filepath.Join(root, link.name)); err != nil {
			t.Fatal(err)
		}
	}
	a, err := New(filepath.Join(root, "token"), testPod)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	if err := os.Symlink("generation2", filepath.Join(root, "..data.new")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "..data.new"), filepath.Join(root, "..data")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := authenticate(a, second); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if _, err := authenticate(a, first); err != nil {
		t.Fatal("previous projected value rejected", err)
	}
}
