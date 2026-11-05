package credentials

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPRefreshFailureLogsOnceAcrossRequestsAndBackoff(t *testing.T) {
	for _, state := range []string{"cold", "expired", "valid"} {
		t.Run(state, func(t *testing.T) {
			clock := &fakeClock{now: time.Now()}
			s := newTestService(clock, &fakeFetch{})
			s.provider = fakeProvider{}
			s.developerAPIEndpoint = "https://example.invalid"
			s.idaasInstanceID = "fixture"
			calls := 0
			s.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("fake-upstream-secret"))}, nil
			})}
			s.fetchCredentials = s.callDeveloperAPI
			wantStatus := http.StatusBadGateway
			if state != "cold" {
				exp := clock.now.Add(-time.Second)
				if state == "valid" {
					exp = clock.now.Add(time.Hour)
					wantStatus = http.StatusOK
				}
				seedEntry(s, testRoleARN, respWithExpiry(vendorAWS, exp.Unix()), exp, clock.now.Add(-time.Minute), time.Time{})
			}
			var output bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&output)
			t.Cleanup(func() { log.SetOutput(previous) })
			h := s.CredentialsHandler(testRoleARN, nil, testRequestAuthenticator{})
			for attempt := 1; attempt <= 2; attempt++ {
				for range 20 {
					if rr := do(t, h, http.MethodGet, "/credentials"); rr.Code != wantStatus {
						t.Fatalf("status=%d, want %d", rr.Code, wantStatus)
					}
				}
				waitForTestRefresh(t, s, testRoleARN)
				if lines := strings.Count(output.String(), "\n"); lines != attempt || calls != attempt {
					t.Fatalf("attempt=%d: upstream calls=%d log lines=%d", attempt, calls, lines)
				}
				clock.now = clock.now.Add(staleRetryBackoff)
			}
			if !strings.Contains(output.String(), "status 503") || strings.Contains(output.String(), "fake-upstream-secret") {
				t.Fatal("failure log lost status or exposed upstream body")
			}
		})
	}
}

func TestConcurrentFailedRefreshLogsOnce(t *testing.T) {
	s := newTestService(&SystemClock{}, &fakeFetch{})
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	cause := errors.New("shared upstream failure")
	s.fetchCredentials = func(context.Context, string) (*developerAPIResponse, error) {
		calls.Add(1)
		close(started)
		<-release
		return nil, cause
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	h := s.CredentialsHandler(testRoleARN, nil, testRequestAuthenticator{})
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rr := do(t, h, http.MethodGet, "/credentials")
			if rr.Code != http.StatusBadGateway {
				t.Errorf("status=%d, want 502", rr.Code)
			}
		}()
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		close(release)
		wg.Wait()
		t.Fatal("shared refresh did not start")
	}
	close(release)
	wg.Wait()
	if lines := strings.Count(output.String(), "\n"); lines != 1 || calls.Load() != 1 {
		t.Fatalf("upstream calls=%d log lines=%d", calls.Load(), lines)
	}
	_, err := s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
	if !errors.Is(err, errCredentialRefreshUnavailable) {
		t.Fatal("backoff did not retain the logged-refresh marker")
	}
}

func TestCacheAdmissionFailureRemainsObservable(t *testing.T) {
	now := time.Now()
	s := newTestService(&SystemClock{}, &fakeFetch{})
	for i := range maxCredentialCacheEntries {
		exp := now.Add(time.Hour)
		seedEntry(s, fmt.Sprintf("retained-role-%d", i), respWithExpiry(vendorAWS, exp.Unix()), exp, exp, time.Time{})
	}
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	rr := do(t, s.CredentialsHandler(testRoleARN, nil, testRequestAuthenticator{}), http.MethodGet, "/credentials")
	if rr.Code != http.StatusBadGateway || !strings.Contains(output.String(), "credential cache full") {
		t.Fatal("cache admission error became silent")
	}
}

func TestRefreshFailureLogsOncePerAttempt(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	fetch := &fakeFetch{err: errors.New("test upstream unavailable")}
	s := newTestService(clock, fetch)
	valid := respWithExpiry(vendorAWS, clock.now.Add(time.Hour).Unix())
	seedEntry(s, testRoleARN, valid, clock.now.Add(time.Hour), clock.now.Add(-time.Second), time.Time{})
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	for attempt := 1; attempt <= 2; attempt++ {
		for range 100 {
			got, err := s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
			if err != nil || got != valid {
				t.Fatalf("still-valid fallback lost: %v", err)
			}
		}
		waitForTestRefresh(t, s, testRoleARN)
		if calls := fetch.count(); calls != attempt {
			t.Fatalf("upstream calls=%d, want %d", calls, attempt)
		}
		if lines := strings.Count(output.String(), "\n"); lines != attempt {
			t.Fatalf("log lines=%d, want one per failed refresh (%d)", lines, attempt)
		}
		if !strings.Contains(output.String(), "test upstream unavailable") {
			t.Fatal("failure reason no longer observable")
		}
		clock.now = clock.now.Add(staleRetryBackoff)
	}
}

func TestRefreshPanicLogsCodeLocationWithoutPayload(t *testing.T) {
	s := newTestService(&SystemClock{}, &fakeFetch{})
	const secret = "fake-sensitive-panic-payload"
	s.fetchCredentials = func(context.Context, string) (*developerAPIResponse, error) { panic(secret) }
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	_, err := s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("panic did not fail safely: %v", err)
	}
	if !strings.Contains(output.String(), "logging_test.go:") || strings.Contains(output.String(), secret) {
		t.Fatalf("expected code location without panic value: %s", output.String())
	}
}
