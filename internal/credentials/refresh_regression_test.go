package credentials

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSlowSuccessfulRefreshIsSharedUntilRemainingLifetimeStales(t *testing.T) {
	start := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := &fakeClock{now: start}
	s := newTestService(clk, &fakeFetch{})
	calls := 0
	s.fetchCredentials = func(context.Context, string) (*developerAPIResponse, error) {
		calls++
		clk.now = clk.now.Add(9 * time.Second)
		return respWithExpiry(vendorAWS, clk.now.Add(time.Second).Unix()), nil
	}
	var first *developerAPIResponse
	for i := 0; i < 16; i++ {
		got, err := s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = got
		} else if got != first {
			t.Errorf("request %d did not reuse the still-valid refresh result", i)
		}
	}
	if calls != 1 {
		t.Fatalf("16 requests made %d upstream calls; want 1", calls)
	}
	// Once the remaining lifetime has elapsed, a new refresh must still run.
	clk.now = start.Add(10 * time.Second)
	if _, err := s.serveDeveloperAPIResponse(context.Background(), testRoleARN); err != nil || calls != 2 {
		t.Fatalf("expired credential was not refreshed: calls=%d err=%v", calls, err)
	}
}

func TestCanceledRequestDoesNotStartRefresh(t *testing.T) {
	fetch := &fakeFetch{resp: respWithExpiry(vendorAWS, time.Now().Add(time.Hour).Unix())}
	s := newTestService(&SystemClock{}, fetch)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.serveDeveloperAPIResponse(ctx, testRoleARN)
	if !errors.Is(err, context.Canceled) || fetch.count() != 0 {
		t.Fatalf("canceled request: err=%v upstream calls=%d; want canceled and 0", err, fetch.count())
	}
}

func TestCanceledWaitersReturnWithoutCancelingSharedRefresh(t *testing.T) {
	s := newTestService(&SystemClock{}, &fakeFetch{})
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s.fetchCredentials = func(ctx context.Context, _ string) (*developerAPIResponse, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return respWithExpiry(vendorAWS, time.Now().Add(time.Hour).Unix()), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	firstDone := make(chan error, 1)
	go func() {
		_, err := s.serveDeveloperAPIResponse(firstCtx, testRoleARN)
		firstDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	cancelFirst()
	select {
	case err := <-firstDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("initiator returned %v; want canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled initiator remained blocked on shared refresh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	waiterDone := make(chan error, 1)
	go func() {
		_, err := s.serveDeveloperAPIResponse(ctx, testRoleARN)
		waiterDone <- err
	}()
	select {
	case err := <-waiterDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("waiter returned %v; want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("expired waiter remained blocked on shared refresh")
	}
	if calls.Load() != 1 {
		t.Fatalf("waiters started %d refreshes; want 1", calls.Load())
	}
	// A live caller must receive the detached result after both others cancel.
	go func() {
		_, err := s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
		waiterDone <- err
	}()
	unblock()
	select {
	case err := <-waiterDone:
		if err != nil {
			t.Fatalf("shared refresh was canceled with its initiator: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("live caller did not receive the shared refresh result")
	}
	if calls.Load() != 1 {
		t.Fatalf("shared result was not cached: %d upstream calls", calls.Load())
	}
}

func TestDetachedRefreshCannotBeEvictedAtCapacity(t *testing.T) {
	s := newTestService(&SystemClock{}, &fakeFetch{})
	exp := time.Now().Add(time.Hour)
	for i := 0; i < maxCredentialCacheEntries-1; i++ {
		seedEntry(s, fmt.Sprintf("role-%d", i), respWithExpiry(vendorAWS, exp.Unix()), exp, exp, time.Time{})
	}
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	s.fetchCredentials = func(context.Context, string) (*developerAPIResponse, error) {
		close(started)
		<-release
		return respWithExpiry(vendorAWS, exp.Unix()), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.serveDeveloperAPIResponse(ctx, testRoleARN)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled request did not return")
	}
	if _, err := s.getOrCreateCredentialEntry("another-role"); err == nil {
		t.Fatal("active detached refresh was evicted to admit another role")
	}
	unblock()
	if _, err := s.serveDeveloperAPIResponse(context.Background(), testRoleARN); err != nil {
		t.Fatal(err)
	}
}

func TestDetachedRefreshPanicFailsClosedAndBacksOff(t *testing.T) {
	s := newTestService(&SystemClock{}, &fakeFetch{})
	calls := 0
	s.fetchCredentials = func(context.Context, string) (*developerAPIResponse, error) {
		calls++
		panic("fake-sensitive-value")
	}
	for i := 0; i < 2; i++ {
		resp, err := s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
		if resp != nil || err == nil || strings.Contains(err.Error(), "fake-sensitive-value") {
			t.Fatalf("panic did not fail closed: resp=%v err=%v", resp, err)
		}
	}
	if calls != 1 {
		t.Fatalf("panic bypassed failure backoff: %d calls", calls)
	}
}

func TestDetachedRefreshStopsAtItsOwnBudget(t *testing.T) {
	s := newTestService(&SystemClock{}, &fakeFetch{})
	s.refreshTimeout = 25 * time.Millisecond
	s.fetchCredentials = func(ctx context.Context, _ string) (*developerAPIResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := s.serveDeveloperAPIResponse(ctx, testRoleARN)
	if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatalf("refresh did not stop within its own budget: err=%v caller=%v", err, ctx.Err())
	}
}
