package credentials

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestFailedRolesAreReclaimedAfterBackoff(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	fetch := &fakeFetch{err: errors.New("role not found")}
	s := newTestService(clk, fetch)
	for i := 0; i < maxCredentialCacheEntries; i++ {
		if _, err := s.serveDeveloperAPIResponse(context.Background(), fmt.Sprintf("missing-role-%d", i)); err == nil {
			t.Fatal("missing role unexpectedly succeeded")
		}
	}
	if _, err := s.serveDeveloperAPIResponse(context.Background(), testRoleARN); err == nil || fetch.count() != maxCredentialCacheEntries {
		t.Fatal("failure backoff must be retained even at capacity")
	}
	clk.now = clk.now.Add(staleRetryBackoff)
	want := respWithExpiry(vendorAlibaba, clk.now.Add(time.Hour).Unix())
	fetch.set(want, nil)
	got, err := s.serveDeveloperAPIResponse(context.Background(), testRoleARN)
	if err != nil || got != want || fetch.count() != maxCredentialCacheEntries+1 {
		t.Fatalf("valid role did not recover after failure entries became reclaimable: %v", err)
	}
	if s.credentialCacheEntryCount != maxCredentialCacheEntries {
		t.Fatalf("entry count = %d", s.credentialCacheEntryCount)
	}
}

func TestCacheReclamationProtectsValidAndBusyEntries(t *testing.T) {
	for _, state := range []string{"expired", "valid", "refreshing"} {
		t.Run(state, func(t *testing.T) {
			clk := &fakeClock{now: time.Now()}
			s := newTestService(clk, &fakeFetch{})
			var entries []*credentialCacheEntry
			for i := 0; i < maxCredentialCacheEntries; i++ {
				exp := clk.now.Add(-time.Second)
				if state == "valid" {
					exp = clk.now.Add(time.Hour)
				}
				e := seedEntry(s, fmt.Sprintf("role-%d", i), respWithExpiry(vendorAlibaba, exp.Unix()), exp, exp.Add(-time.Minute), time.Time{})
				entries = append(entries, e)
				if state == "refreshing" {
					e.mu.Lock()
					defer e.mu.Unlock()
				}
			}
			_, err := s.getOrCreateCredentialEntry("new-role")
			if (err == nil) != (state == "expired") {
				t.Fatalf("state=%s: unexpected admission result: %v", state, err)
			}
			evicted := 0
			for _, e := range entries {
				if e.evicted {
					evicted++
				}
			}
			if state == "expired" && evicted != 1 || state != "expired" && evicted != 0 {
				t.Fatalf("state=%s: evicted %d entries", state, evicted)
			}
		})
	}
}

func TestConcurrentCacheAdmissionStaysBounded(t *testing.T) {
	clk := &fakeClock{now: time.Now()}
	s := newTestService(clk, &fakeFetch{resp: respWithExpiry(vendorAlibaba, clk.now.Add(time.Hour).Unix())})
	// Empty entries can be reclaimed; successfully fetched, unexpired credentials cannot.
	for i := 0; i < maxCredentialCacheEntries; i++ {
		if _, err := s.getOrCreateCredentialEntry(fmt.Sprintf("empty-role-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 2*maxCredentialCacheEntries; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = s.serveDeveloperAPIResponse(context.Background(), fmt.Sprintf("new-role-%d", i))
		}(i)
	}
	wg.Wait()
	count := 0
	s.credentialCache.Range(func(_, value any) bool {
		count++
		if value.(*credentialCacheEntry).resp == nil {
			t.Error("empty entry retained instead of a successful credential")
		}
		return true
	})
	if count != maxCredentialCacheEntries || s.credentialCacheEntryCount != count {
		t.Fatalf("cache count=%d, tracked count=%d", count, s.credentialCacheEntryCount)
	}
}
