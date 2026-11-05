package credentials

// Controlled time and upstream responses exercise refresh, backoff and expiry.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

// fakeFetch counts upstream calls and returns the configured response.
type fakeFetch struct {
	resp  *developerAPIResponse
	err   error
	calls int32
}

func (f *fakeFetch) fn(_ context.Context, _ string) (*developerAPIResponse, error) {
	atomic.AddInt32(&f.calls, 1)
	return f.resp, f.err
}

func (f *fakeFetch) count() int { return int(atomic.LoadInt32(&f.calls)) }

func (f *fakeFetch) set(resp *developerAPIResponse, err error) { f.resp = resp; f.err = err }

// newTestService replaces time and upstream calls without initializing the SDK.
func newTestService(clk Clock, fetch *fakeFetch) *Service {
	s := &Service{clock: clk}
	s.fetchCredentials = fetch.fn
	return s
}

// respWithExpiry constructs a complete DTO with consistent absolute expiry.
func respWithExpiry(vendor string, expUnix int64) *developerAPIResponse {
	ac := &accessCredentialEnvelope{AccessCredentialExpiresAt: expUnix}
	expiration := time.Unix(expUnix, 0).UTC().Format(time.RFC3339)
	switch vendor {
	case vendorAlibaba:
		ac.AlibabaCloudSTSToken = &alibabaSTSEnvelope{AccessKeyID: "fake-id", AccessKeySecret: "fake-secret", SecurityToken: "fake-token", Expiration: expiration}
	case vendorAWS:
		ac.AWSSTSToken = &awsSTSEnvelope{AccessKeyID: "fake-id", SecretAccessKey: "fake-secret", SessionToken: "fake-token", Expiration: expiration}
	case vendorTencent:
		ac.TencentCloudSTSToken = &tencentSTSEnvelope{TmpSecretID: "fake-id", TmpSecretKey: "fake-secret", Token: "fake-token", Expiration: expiration, ExpiredTime: &expUnix}
	}
	return &developerAPIResponse{CloudAccountVendorType: vendor, CloudAccountRoleAccessCredential: ac}
}

const testRoleARN = "acs:ram::123:role/test"

// seedEntry 直接写 entry 的未导出寿命字段(白盒),模拟"已有缓存"前置态。
func seedEntry(s *Service, roleARN string, resp *developerAPIResponse, exp, stale, lastFail time.Time) *credentialCacheEntry {
	e, _ := s.getOrCreateCredentialEntry(roleARN)
	e.resp = resp
	e.expiresAt = exp
	e.staleTime = stale
	e.lastFailedRefreshAt = lastFail
	return e
}

// --- 时间线场景:一条 roleARN 走完整寿命周期 ---

func TestSTSStateMachineTimeline(t *testing.T) {
	T0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ttl := 1000 * time.Second
	// The default margin (300s) plus refresh budget (10s) advances stale to T0+690.

	clk := &fakeClock{now: T0}
	fetch := &fakeFetch{}
	s := newTestService(clk, fetch)

	// Step1: T0,缓存未命中 → 刷新成功 respA(exp=T0+1000, stale=T0+690)。
	clk.now = T0
	fetch.set(respWithExpiry("alibaba_cloud", T0.Add(ttl).Unix()), nil)
	got, err := s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
	if err != nil || got.CloudAccountVendorType != "alibaba_cloud" {
		t.Fatalf("step1: want respA nil-err, got %v %v", got, err)
	}
	if fetch.count() != 1 {
		t.Fatalf("step1: want 1 fetch, got %d", fetch.count())
	}
	e, _ := s.getOrCreateCredentialEntry(testRoleARN)
	if !e.expiresAt.Equal(T0.Add(ttl)) || !e.staleTime.Equal(T0.Add(690*time.Second)) || !e.lastFailedRefreshAt.IsZero() {
		t.Fatalf("step1: cache state mismatch: exp=%s stale=%s lastFail=%v",
			e.expiresAt, e.staleTime, e.lastFailedRefreshAt)
	}

	// Step2: T0+100,缓存新鲜(now < staleTime=T0+690)→ 命中不刷新。
	clk.now = T0.Add(100 * time.Second)
	fetch.calls = 0
	got, err = s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
	if err != nil || got != e.resp {
		t.Fatalf("step2: want cached respA no-fetch, got %v %v", got, err)
	}
	if fetch.count() != 0 {
		t.Fatalf("step2: want 0 fetch, got %d", fetch.count())
	}

	// Step3: T0+850,余量低于门槛 → 等待刷新 respC(exp=T0+1850, stale=T0+1540)。
	clk.now = T0.Add(850 * time.Second)
	fetch.calls = 0
	respC := respWithExpiry("alibaba_cloud", T0.Add(1850*time.Second).Unix())
	fetch.set(respC, nil)
	got, err = s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
	if err != nil || got != respC {
		t.Fatalf("step3: want respC nil-err, got %v %v", got, err)
	}
	if fetch.count() != 1 {
		t.Fatalf("step3: want 1 fetch, got %d", fetch.count())
	}
	if !e.expiresAt.Equal(T0.Add(1850*time.Second)) || !e.staleTime.Equal(T0.Add(1540*time.Second)) {
		t.Fatalf("step3: cache state mismatch: exp=%s stale=%s", e.expiresAt, e.staleTime)
	}

	// Step4: T0+1660,过 staleTime=T0+1540 → 刷新失败,旧缓存仍有效(< exp=T0+1850)→ 降级返 respC。
	clk.now = T0.Add(1660 * time.Second)
	fetch.calls = 0
	fetch.set(nil, errors.New("upstream down"))
	got, err = s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
	if err != nil || got != respC {
		t.Fatalf("step4: want stale respC nil-err, got %v %v", got, err)
	}
	if fetch.count() != 1 {
		t.Fatalf("step4: want 1 fetch, got %d", fetch.count())
	}
	if !e.lastFailedRefreshAt.Equal(T0.Add(1660 * time.Second)) {
		t.Fatalf("step4: lastFailedRefreshAt=%v, want T0+1660", e.lastFailedRefreshAt)
	}

	// Step5: T0+1665,退避窗内(5s < 10s)且旧缓存有效 → 降级返 respC,不重试上游刷新。
	clk.now = T0.Add(1665 * time.Second)
	fetch.calls = 0
	got, err = s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
	if err != nil || got != respC {
		t.Fatalf("step5: want stale respC no-fetch, got %v %v", got, err)
	}
	if fetch.count() != 0 {
		t.Fatalf("step5: want 0 fetch in backoff, got %d", fetch.count())
	}

	// Step6: T0+1672,退避已过(12s > 10s)→ 重新刷新失败,旧缓存仍有效 → 降级返 respC。
	clk.now = T0.Add(1672 * time.Second)
	fetch.calls = 0
	fetch.set(nil, errors.New("upstream down"))
	got, err = s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
	if err != nil || got != respC {
		t.Fatalf("step6: want stale respC nil-err, got %v %v", got, err)
	}
	if fetch.count() != 1 {
		t.Fatalf("step6: want 1 fetch, got %d", fetch.count())
	}
	if !e.lastFailedRefreshAt.Equal(T0.Add(1672 * time.Second)) {
		t.Fatalf("step6: lastFailedRefreshAt=%v, want T0+1672", e.lastFailedRefreshAt)
	}

	// Step7: T0+1900,旧缓存已过期；本次刷新也失败，不能再降级返回过期凭据。
	clk.now = T0.Add(1900 * time.Second)
	fetch.calls = 0
	fetch.set(nil, errors.New("upstream down"))
	got, err = s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
	if err == nil {
		t.Fatalf("step7: want fail-fast error, got %v", got)
	}
	if fetch.count() != 1 {
		t.Fatalf("step7: want 1 fetch, got %d", fetch.count())
	}
}

// --- 边界用例表 ---

func TestSTSStateMachineEdgeCases(t *testing.T) {
	T0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	oneResp := respWithExpiry("alibaba_cloud", T0.Add(1000*time.Second).Unix())

	type tc struct {
		name string
		// 前置缓存(nil = 无缓存)
		seedResp   *developerAPIResponse
		seedExp    time.Time
		seedStale  time.Time
		seedLastF  time.Time
		now        time.Time
		fetchResp  *developerAPIResponse
		fetchErr   error
		wantErr    bool
		wantStale  bool // true = 期望返回旧缓存(seedResp);false = 期望返回 fetchResp 或无返回
		wantFetch  int  // 期望 fetch 调用次数
		wantNoCach bool // true = 期望刷新产物未入库(过期/缺过期时间不缓存)
	}
	cases := []tc{
		{
			name:     "fresh cache no refresh",
			seedResp: oneResp, seedExp: T0.Add(1000 * time.Second), seedStale: T0.Add(800 * time.Second),
			now: T0.Add(100 * time.Second), wantFetch: 0, wantStale: true,
		},
		{
			name:     "stale + refresh success",
			seedResp: oneResp, seedExp: T0.Add(1000 * time.Second), seedStale: T0.Add(800 * time.Second),
			now: T0.Add(850 * time.Second), fetchResp: respWithExpiry("aws", T0.Add(1850*time.Second).Unix()),
			wantFetch: 1, wantStale: false,
		},
		{
			name:     "stale + refresh fail + stale fallback",
			seedResp: oneResp, seedExp: T0.Add(1850 * time.Second), seedStale: T0.Add(1650 * time.Second),
			now: T0.Add(1660 * time.Second), fetchErr: errors.New("down"),
			wantErr: false, wantStale: true, wantFetch: 1,
		},
		{
			name:     "truly expired + fail fast",
			seedResp: oneResp, seedExp: T0.Add(1000 * time.Second), seedStale: T0.Add(800 * time.Second),
			now: T0.Add(1100 * time.Second), fetchErr: errors.New("down"),
			wantErr: true, wantFetch: 1,
		},
		{
			name: "no cache + refresh fail",
			now:  T0, fetchErr: errors.New("down"),
			wantErr: true, wantFetch: 1,
		},
		{
			name: "no cache + missing expiry",
			now:  T0, fetchResp: &developerAPIResponse{
				CloudAccountRoleAccessCredential: &accessCredentialEnvelope{AccessCredentialExpiresAt: 0},
			},
			wantErr: true, wantFetch: 1, wantNoCach: true,
		},
		{
			name: "no cache + already expired credential",
			now:  T0, fetchResp: respWithExpiry("aws", T0.Add(-100*time.Second).Unix()),
			wantErr: true, wantFetch: 1, wantNoCach: true,
		},
		{
			name:     "backoff window + valid stale",
			seedResp: oneResp, seedExp: T0.Add(1850 * time.Second), seedStale: T0.Add(1650 * time.Second),
			seedLastF: T0.Add(1660 * time.Second), now: T0.Add(1665 * time.Second),
			wantErr: false, wantStale: true, wantFetch: 0,
		},
		{
			name:     "backoff window + expired fail fast",
			seedResp: oneResp, seedExp: T0.Add(1000 * time.Second), seedStale: T0.Add(800 * time.Second),
			seedLastF: T0.Add(1000 * time.Second), now: T0.Add(1005 * time.Second),
			wantErr: true, wantFetch: 0,
		},
		{
			name:     "backoff expired + refresh fail + stale fallback",
			seedResp: oneResp, seedExp: T0.Add(1850 * time.Second), seedStale: T0.Add(1650 * time.Second),
			seedLastF: T0.Add(1660 * time.Second), now: T0.Add(1672 * time.Second),
			fetchErr: errors.New("down"), wantErr: false, wantStale: true, wantFetch: 1,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clk := &fakeClock{now: c.now}
			fetch := &fakeFetch{resp: c.fetchResp, err: c.fetchErr}
			s := newTestService(clk, fetch)
			if c.seedResp != nil {
				seedEntry(s, testRoleARN, c.seedResp, c.seedExp, c.seedStale, c.seedLastF)
			}
			got, err := s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
			if fetch.count() != c.wantFetch {
				t.Fatalf("fetch calls = %d, want %d", fetch.count(), c.wantFetch)
			}
			if c.wantErr && err == nil {
				t.Fatalf("want error, got nil (resp=%v)", got)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("want no error, got %v", err)
			}
			if c.wantNoCach {
				e, _ := s.getOrCreateCredentialEntry(testRoleARN)
				if e.resp != nil {
					// 刷新产物不该入库:过期/缺过期时间的凭据不能被缓存(守住"不缓存过期 STS")
					t.Fatalf("expected no cache of invalid credential, resp=%v exp=%s", e.resp, e.expiresAt)
				}
			}
			if !c.wantErr {
				var want *developerAPIResponse
				if c.wantStale {
					want = c.seedResp
				} else {
					want = c.fetchResp
				}
				if got != want {
					t.Fatalf("resp mismatch: got %v, want %v", got, want)
				}
			}
		})
	}
}

// --- 并发刷新去重:同 roleARN 并发只触发一次 fetch ---

func TestSTSStateMachineSingleFlight(t *testing.T) {
	T0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := &fakeClock{now: T0}
	fetch := &fakeFetch{}
	fetch.set(respWithExpiry("alibaba_cloud", T0.Add(1000*time.Second).Unix()), nil)
	s := newTestService(clk, fetch)

	const n = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-start
			_, _ = s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
		}()
	}
	close(start)
	wg.Wait()

	if fetch.count() != 1 {
		t.Fatalf("single-flight: want 1 fetch under %d concurrent, got %d", n, fetch.count())
	}
}

func TestSlowRefreshFailureStartsFullBackoffAtCompletion(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprintf("cached=%v", cached), func(t *testing.T) {
			start := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			clk := &fakeClock{now: start}
			s := newTestService(clk, &fakeFetch{})
			var want *developerAPIResponse
			if cached {
				want = respWithExpiry(vendorAlibaba, start.Add(time.Hour).Unix())
				seedEntry(s, testRoleARN, want, start.Add(time.Hour), start.Add(-time.Second), time.Time{})
			}
			calls := 0
			s.fetchCredentials = func(context.Context, string) (*developerAPIResponse, error) {
				calls++
				clk.now = clk.now.Add(defaultCredentialRefreshTimeout)
				return nil, context.DeadlineExceeded
			}
			for i := 0; i < 3; i++ {
				got, err := s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
				waitForTestRefresh(t, s, testRoleARN)
				if cached && (err != nil || got != want) || !cached && err == nil {
					t.Fatalf("unexpected fallback: got=%v err=%v", got, err)
				}
			}
			if calls != 1 {
				t.Fatalf("queued requests repeated a timed-out refresh: %d calls", calls)
			}
			clk.now = clk.now.Add(staleRetryBackoff - time.Nanosecond)
			_, _ = s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
			if calls != 1 {
				t.Fatal("retried before completion-based backoff elapsed")
			}
			clk.now = clk.now.Add(time.Nanosecond)
			_, _ = s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
			waitForTestRefresh(t, s, testRoleARN)
			if calls != 2 {
				t.Fatal("did not retry after the full backoff")
			}
		})
	}
}
