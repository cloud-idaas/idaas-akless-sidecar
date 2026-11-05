package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitForTestRefresh joins a worker without initiating another fetch. Tests
// changing fake time, responses or global log output must first join it.
func waitForTestRefresh(t *testing.T, s *Service, role string) {
	t.Helper()
	v, ok := s.credentialCache.Load(role)
	if !ok {
		t.Fatal("missing cache entry")
	}
	entry := v.(*credentialCacheEntry)
	entry.mu.Lock()
	call := entry.refresh
	entry.mu.Unlock()
	if call != nil {
		select {
		case <-call.done:
		case <-time.After(time.Second):
			t.Fatal("refresh did not finish")
		}
	}
}

func waitForTestRefreshStart(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh was not started")
	}
}

type refreshTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *refreshTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *refreshTestClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestBackgroundRefreshReturnsCachedHTTPContracts(t *testing.T) {
	for _, vendor := range []string{vendorAlibaba, vendorAWS, vendorTencent} {
		for _, suffix := range []string{"", "?format=raw"} {
			t.Run(vendor+suffix, func(t *testing.T) {
				now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
				old := respWithExpiry(vendor, now.Add(10*time.Minute).Unix())
				updated := respWithExpiry(vendor, now.Add(time.Hour).Unix())
				s := newTestService(&fakeClock{now: now}, &fakeFetch{})
				seedEntry(s, testRoleARN, old, now.Add(10*time.Minute), now.Add(-time.Second), time.Time{})
				started, release := make(chan struct{}), make(chan struct{})
				var calls atomic.Int32
				s.fetchCredentials = func(ctx context.Context, _ string) (*developerAPIResponse, error) {
					if calls.Add(1) == 1 {
						close(started)
					}
					select {
					case <-release:
						return updated, nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				var once sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				t.Cleanup(func() { unblock(); waitForTestRefresh(t, s, testRoleARN) })
				handler := s.CredentialsHandler(testRoleARN, nil, testRequestAuthenticator{})
				var expected any = old
				if suffix == "" {
					var err error
					expected, err = convertToCloudContract(old)
					if err != nil {
						t.Fatal(err)
					}
				}
				body, err := json.Marshal(expected)
				if err != nil {
					t.Fatal(err)
				}
				// The upstream remains blocked until every caller has returned.
				var wg sync.WaitGroup
				for range 32 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						response := do(t, handler, http.MethodGet, "/credentials"+suffix)
						if response.Code != http.StatusOK || response.Body.String() != string(body)+"\n" {
							t.Errorf("cached response changed or waited for upstream: status=%d", response.Code)
						}
					}()
				}
				returned := make(chan struct{})
				go func() { wg.Wait(); close(returned) }()
				select {
				case <-returned:
				case <-time.After(time.Second):
					unblock()
					<-returned
					t.Fatal("cached HTTP requests blocked on refresh")
				}
				waitForTestRefreshStart(t, started)
				if calls.Load() != 1 {
					t.Fatalf("concurrent callers started %d refreshes", calls.Load())
				}
				unblock()
				waitForTestRefresh(t, s, testRoleARN)
				got, err := s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
				if err != nil || got != updated || calls.Load() != 1 {
					t.Fatalf("background result was not reused: err=%v calls=%d", err, calls.Load())
				}
			})
		}
	}
}

func TestBackgroundRefreshMinimumValidityBoundary(t *testing.T) {
	for _, tc := range []struct {
		name      string
		remaining time.Duration
		cold      bool
	}{
		{"cold", 0, true}, {"expired", 0, false},
		{"near expiry", 119 * time.Second, false}, {"exact margin", 2 * time.Minute, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := &refreshTestClock{now: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}
			s := newTestService(clock, &fakeFetch{})
			s.refreshMinValidity = 2 * time.Minute
			if !tc.cold {
				exp := clock.Now().Add(tc.remaining)
				seedEntry(s, testRoleARN, respWithExpiry(vendorAWS, exp.Unix()), exp, clock.Now().Add(-time.Second), time.Time{})
			}
			started, release := make(chan struct{}), make(chan struct{})
			updated := respWithExpiry(vendorAWS, clock.Now().Add(time.Hour).Unix())
			var calls atomic.Int32
			s.fetchCredentials = func(ctx context.Context, _ string) (*developerAPIResponse, error) {
				if calls.Add(1) == 1 {
					close(started)
				}
				select {
				case <-release:
					return updated, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			defer func() { close(release); waitForTestRefresh(t, s, testRoleARN) }()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			got, err := s.serveDeveloperAPIResponse(ctx, testRoleARN)
			if got != nil || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("cold/near-expiry request must wait: err=%v", err)
			}
			waitForTestRefreshStart(t, started)
			if calls.Load() != 1 {
				t.Fatalf("unexpected fetch count: %d", calls.Load())
			}
		})
	}
}

func TestActiveBackgroundRefreshSwitchesToWaitingAtMargin(t *testing.T) {
	clock := &refreshTestClock{now: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := newTestService(clock, &fakeFetch{})
	s.refreshMinValidity = time.Minute
	exp := clock.Now().Add(time.Minute + time.Second)
	old := respWithExpiry(vendorAWS, exp.Unix())
	seedEntry(s, testRoleARN, old, exp, clock.Now().Add(-time.Second), time.Time{})
	started, release := make(chan struct{}), make(chan struct{})
	updated := respWithExpiry(vendorAWS, clock.Now().Add(time.Hour).Unix())
	var calls atomic.Int32
	s.fetchCredentials = func(ctx context.Context, _ string) (*developerAPIResponse, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return updated, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer func() { unblock(); waitForTestRefresh(t, s, testRoleARN) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	got, err := s.serveDeveloperAPIResponse(ctx, testRoleARN)
	cancel() // The caller returning cached STS must not cancel its worker.
	if err != nil || got != old {
		t.Fatalf("usable cache did not return immediately: %v", err)
	}
	waitForTestRefreshStart(t, started)
	clock.advance(time.Second)
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if got, err := s.serveDeveloperAPIResponse(ctx, testRoleARN); got != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("active refresh returned near-expiry cache: %v", err)
	}
	unblock()
	waitForTestRefresh(t, s, testRoleARN)
	got, err = s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
	if err != nil || got != updated || calls.Load() != 1 {
		t.Fatalf("canceled waiter affected shared refresh: %v calls=%d", err, calls.Load())
	}
}

func TestBackgroundFailureBackoffRejectsExpiredCache(t *testing.T) {
	clock := &refreshTestClock{now: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := newTestService(clock, &fakeFetch{})
	s.refreshMinValidity = time.Second
	exp := clock.Now().Add(5 * time.Second)
	old := respWithExpiry(vendorAWS, exp.Unix())
	entry := seedEntry(s, testRoleARN, old, exp, clock.Now().Add(-time.Second), time.Time{})
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s.fetchCredentials = func(context.Context, string) (*developerAPIResponse, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return nil, errors.New("upstream unavailable")
	}
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer func() { unblock(); waitForTestRefresh(t, s, testRoleARN) }()
	if got, err := s.serveDeveloperAPIResponse(context.Background(), testRoleARN); err != nil || got != old {
		t.Fatalf("cached credential unavailable: %v", err)
	}
	waitForTestRefreshStart(t, started)
	clock.advance(2 * time.Second)
	unblock()
	waitForTestRefresh(t, s, testRoleARN)
	entry.mu.Lock()
	failedAt := entry.lastFailedRefreshAt
	entry.mu.Unlock()
	if !failedAt.Equal(clock.Now()) {
		t.Fatalf("backoff did not start at completion: %s", failedAt)
	}
	if got, err := s.serveDeveloperAPIResponse(context.Background(), testRoleARN); err != nil || got != old {
		t.Fatalf("valid fallback lost in backoff: %v", err)
	}
	clock.advance(3 * time.Second)
	if got, err := s.serveDeveloperAPIResponse(context.Background(), testRoleARN); got != nil || !errors.Is(err, errCredentialRefreshUnavailable) {
		t.Fatalf("expired cache escaped backoff gate: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("backoff caused repeated calls: %d", calls.Load())
	}
	clock.advance(staleRetryBackoff - 3*time.Second)
	_, _ = s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
	if calls.Load() != 2 {
		t.Fatalf("refresh did not resume after backoff: %d", calls.Load())
	}
}

func TestRefreshPointReservesUsableCacheAndBudget(t *testing.T) {
	for _, tc := range []struct {
		name            string
		ttl, margin     time.Duration
		budget, staleIn time.Duration
	}{
		{"long lived", time.Hour, 5 * time.Minute, 10 * time.Second, 48 * time.Minute},
		{"advance refresh", 15 * time.Minute, 5 * time.Minute, 10 * time.Second, 590 * time.Second},
		{"custom margin", 10 * time.Minute, time.Minute, 10 * time.Second, 8 * time.Minute},
		{"short lived", time.Second, 5 * time.Minute, 10 * time.Second, 800 * time.Millisecond},
		{"no asynchronous window", 310 * time.Second, 5 * time.Minute, 10 * time.Second, 248 * time.Second},
		{"large margin", time.Hour, time.Duration(1<<63 - 1), 10 * time.Second, 48 * time.Minute},
		{"large budget", time.Hour, 5 * time.Minute, time.Duration(1<<63 - 1), 48 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			fetch := &fakeFetch{resp: respWithExpiry(vendorAWS, now.Add(tc.ttl).Unix())}
			s := newTestService(&fakeClock{now: now}, fetch)
			s.refreshMinValidity, s.refreshTimeout = tc.margin, tc.budget
			if _, err := s.serveDeveloperAPIResponse(context.Background(), testRoleARN); err != nil {
				t.Fatal(err)
			}
			entry, _ := s.getOrCreateCredentialEntry(testRoleARN)
			entry.mu.Lock()
			stale := entry.staleTime
			entry.mu.Unlock()
			if !stale.Equal(now.Add(tc.staleIn)) {
				t.Fatalf("refresh point=%s, want %s", stale, now.Add(tc.staleIn))
			}
		})
	}
}

func TestInvalidBackgroundRefreshKeepsValidatedCache(t *testing.T) {
	for _, failure := range []string{"incomplete", "unsupported", "expired", "panic"} {
		t.Run(failure, func(t *testing.T) {
			now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			old := respWithExpiry(vendorAWS, now.Add(10*time.Minute).Unix())
			invalid := respWithExpiry(vendorAWS, now.Add(time.Hour).Unix())
			switch failure {
			case "incomplete":
				invalid.CloudAccountRoleAccessCredential.AWSSTSToken.SessionToken = ""
			case "unsupported":
				invalid.CloudAccountVendorType = "unsupported"
			case "expired":
				invalid = respWithExpiry(vendorAWS, now.Unix())
			}
			s := newTestService(&fakeClock{now: now}, &fakeFetch{})
			entry := seedEntry(s, testRoleARN, old, now.Add(10*time.Minute), now.Add(-time.Second), time.Time{})
			var calls atomic.Int32
			s.fetchCredentials = func(context.Context, string) (*developerAPIResponse, error) {
				calls.Add(1)
				if failure == "panic" {
					panic("sensitive refresh failure")
				}
				return invalid, nil
			}
			if got, err := s.serveDeveloperAPIResponse(context.Background(), testRoleARN); err != nil || got != old {
				t.Fatalf("background refresh lost usable response: %v", err)
			}
			waitForTestRefresh(t, s, testRoleARN)
			entry.mu.Lock()
			cached, failedAt := entry.resp, entry.lastFailedRefreshAt
			entry.mu.Unlock()
			if cached != old || !failedAt.Equal(now) {
				t.Fatal("invalid refresh poisoned cache or skipped backoff")
			}
			if got, err := s.serveDeveloperAPIResponse(context.Background(), testRoleARN); err != nil || got != old || calls.Load() != 1 {
				t.Fatalf("failed background refresh bypassed fallback/backoff: %v calls=%d", err, calls.Load())
			}
		})
	}
}
