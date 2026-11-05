package projectedauth

import (
	"bytes"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func captureAuthenticationLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &output
}

func TestReferenceFailureDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason referenceFailure
		change func(*testing.T, string, *time.Time)
	}{
		{"missing", reasonFileMissing, func(t *testing.T, path string, _ *time.Time) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}},
		{"permissions", reasonUnsafePermissions, func(t *testing.T, path string, _ *time.Time) {
			if err := os.Chmod(path, 0660); err != nil {
				t.Fatal(err)
			}
		}},
		{"oversized", reasonInvalidFile, func(t *testing.T, path string, _ *time.Time) {
			publish(t, path, strings.Repeat("s", MaxTokenBytes+1))
		}},
		{"invalid format", reasonInvalidToken, func(t *testing.T, path string, _ *time.Time) {
			publish(t, path, "sensitive-file-content")
		}},
		{"invalid JWT", reasonInvalidToken, func(t *testing.T, path string, _ *time.Time) {
			publish(t, path, "a.b.c")
		}},
		{"binding", reasonInvalidBinding, func(t *testing.T, path string, now *time.Time) {
			claims := claimsAt(*now, "sensitive-claim")
			claims["sub"] = "sensitive-subject"
			publish(t, path, encodeClaims(t, claims))
		}},
		{"missing validity", reasonInvalidValidity, func(t *testing.T, path string, now *time.Time) {
			claims := claimsAt(*now, "sensitive-claim")
			delete(claims, "nbf")
			publish(t, path, encodeClaims(t, claims))
		}},
		{"future validity", reasonInvalidValidity, func(t *testing.T, path string, now *time.Time) {
			claims := claimsAt(*now, "sensitive-claim")
			claims["iat"] = now.Add(time.Minute).Unix()
			publish(t, path, encodeClaims(t, claims))
		}},
		{"expired", reasonExpired, func(_ *testing.T, _ string, now *time.Time) {
			*now = now.Add(10 * time.Minute)
		}},
		{"maximum age", reasonMaximumAgeExceeded, func(t *testing.T, path string, now *time.Time) {
			claims := claimsAt(*now, "sensitive-claim")
			claims["exp"] = now.Add(24 * time.Hour).Unix()
			publish(t, path, encodeClaims(t, claims))
			*now = now.Add(time.Hour)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, path, token, now := fixture(t)
			output := captureAuthenticationLogs(t)
			tc.change(t, path, now)
			for range 20 {
				_, err := authenticate(a, token)
				var failure referenceFailure
				if !errors.Is(err, ErrUnavailable) || !errors.As(err, &failure) || failure != tc.reason {
					t.Fatalf("error=%v, want reason=%s", err, string(tc.reason))
				}
			}
			if a.current != (record{}) || a.previous != (record{}) {
				t.Fatal("failed reference retained authentication state")
			}
			if strings.Count(output.String(), "\n") != 1 || !strings.Contains(output.String(), "WARN local credential authentication unavailable: reason="+string(tc.reason)+"\n") {
				t.Fatalf("missing diagnostic or repeated outage log: %s", output)
			}
			for _, secret := range []string{token, path, "sensitive", testPod.UID, testPod.Namespace, "ParseUnverified"} {
				if strings.Contains(output.String(), secret) {
					t.Fatalf("authentication diagnostic exposed %q", secret)
				}
			}
			recovered := encodeClaims(t, claimsAt(*now, "recovered"))
			publish(t, path, recovered)
			for range 20 {
				check, err := authenticate(a, recovered)
				if err != nil || check() != nil {
					t.Fatalf("valid reference did not recover: %v", err)
				}
			}
			if strings.Count(output.String(), "\n") != 2 || strings.Count(output.String(), "local credential authentication recovered") != 1 {
				t.Fatalf("recovery missing or repeated: %s", output)
			}
		})
	}
}

func TestReferenceDiagnosticsRateLimit(t *testing.T) {
	a, path, token, now := fixture(t)
	output := captureAuthenticationLogs(t)
	fail := func(wantLines int) {
		t.Helper()
		if _, err := authenticate(a, token); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("invalid reference accepted: %v", err)
		}
		if strings.Count(output.String(), "\n") != wantLines {
			t.Fatalf("unexpected diagnostic count: %s", output)
		}
	}
	remove := func() {
		t.Helper()
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	remove()
	fail(1)
	publish(t, path, token)
	if _, err := authenticate(a, token); err != nil {
		t.Fatal(err)
	}
	remove()
	fail(2) // Rapid flapping must not produce another failure/recovery pair.
	publish(t, path, token)
	if _, err := authenticate(a, token); err != nil {
		t.Fatal(err)
	}
	remove()
	*now = now.Add(authenticationLogInterval - time.Nanosecond)
	fail(2)
	*now = now.Add(time.Nanosecond)
	fail(3)
	publish(t, path, "a.b.c")
	fail(3) // A new reason is deferred until the rate limit permits it.
	*now = now.Add(authenticationLogInterval)
	fail(4)
	if !strings.Contains(output.String(), "reason=invalid_token\n") {
		t.Fatalf("persistent changed reason was lost: %s", output)
	}
	*now = now.Add(authenticationLogInterval)
	fail(4) // An unchanged outage stays quiet even after the interval.
}

func TestReferenceDiagnosticsConcurrentFinalChecks(t *testing.T) {
	a, path, token, _ := fixture(t)
	check, err := authenticate(a, token)
	if err != nil {
		t.Fatal(err)
	}
	output := captureAuthenticationLogs(t)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := check(); !errors.Is(err, ErrUnavailable) {
				t.Errorf("final check accepted invalid reference: %v", err)
			}
		}()
	}
	wg.Wait()
	if strings.Count(output.String(), "\n") != 1 || !strings.Contains(output.String(), "reason=file_missing\n") {
		t.Fatalf("concurrent final checks amplified logs: %s", output)
	}
}

func TestReferenceDiagnosticErrorsAreSafe(t *testing.T) {
	for _, tc := range []struct {
		cause error
		want  referenceFailure
	}{
		{os.ErrNotExist, reasonFileMissing}, {os.ErrPermission, reasonFilePermissionDenied},
		{errors.New("sensitive IO error"), reasonFileIOFailed},
	} {
		err := referenceIOFailure(tc.cause)
		if err != tc.want || !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "sensitive") {
			t.Fatalf("unsafe or incorrect diagnostic: %v", err)
		}
	}
	for _, tc := range []struct {
		path string
		pod  PodContext
		want referenceFailure
	}{
		{"relative/token", testPod, reasonInvalidConfiguration},
		{filepath.Join(t.TempDir(), "missing", "token"), testPod, reasonFileMissing},
		{"/token", PodContext{}, reasonInvalidConfiguration},
	} {
		_, err := New(tc.path, tc.pod)
		if !errors.Is(err, tc.want) || strings.Contains(err.Error(), tc.path) {
			t.Fatalf("unsafe or incorrect startup diagnostic: %v", err)
		}
	}
	a, _, _, now := fixture(t)
	output := captureAuthenticationLogs(t)
	a.logReferenceStatus(*now, errors.New("sensitive parser error"))
	if !strings.Contains(output.String(), "reason=file_io_failed\n") || strings.Contains(output.String(), "sensitive") {
		t.Fatalf("unexpected error text exposed in diagnostic: %s", output)
	}
}

func TestReferenceDiagnosticsHealthyRequestsStayQuiet(t *testing.T) {
	a, path, token, now := fixture(t)
	output := captureAuthenticationLogs(t)
	for _, tc := range []struct {
		token string
		want  error
	}{
		{token, nil}, {"", ErrUnauthorized}, {"a.b.c", ErrUnauthorized},
		{"Bearer " + token, ErrInvalidRequest},
	} {
		if _, err := authenticate(a, tc.token); !errors.Is(err, tc.want) {
			t.Fatalf("error=%v, want=%v", err, tc.want)
		}
	}
	rotated := encodeClaims(t, claimsAt(*now, "rotated"))
	publish(t, path, rotated)
	check, err := authenticate(a, rotated)
	if err != nil || check() != nil {
		t.Fatalf("normal rotation failed: %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("healthy reference or rejected request generated diagnostics: %s", output)
	}
}
