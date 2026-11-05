package credentials

import (
	"fmt"
	"sync"
	"time"
)

// credentialCacheEntry holds one role's response and refresh state under mu.
type credentialCacheEntry struct {
	mu                  sync.Mutex
	resp                *developerAPIResponse
	refresh             *credentialRefresh
	evicted             bool      // guarded by mu; waiters retry against the current cache entry
	expiresAt           time.Time // accessCredentialExpiresAt, converted from Unix seconds
	staleTime           time.Time // proportional refresh point, advanced when the usable-cache margin needs more room
	lastFailedRefreshAt time.Time // failure completion, used for backoff
}

// A refresh result is immutable once done is closed, so all waiters share it.
type credentialRefresh struct {
	done chan struct{}
	resp *developerAPIResponse
	err  error
}

// getOrCreateCredentialEntry reclaims idle, invalid entries outside backoff at capacity.
// Callers must lock the returned entry and check evicted before using it.
func (s *Service) getOrCreateCredentialEntry(roleARN string) (*credentialCacheEntry, error) {
	if v, ok := s.credentialCache.Load(roleARN); ok {
		return v.(*credentialCacheEntry), nil
	}
	s.credentialCacheMu.Lock()
	defer s.credentialCacheMu.Unlock()
	if v, ok := s.credentialCache.Load(roleARN); ok {
		return v.(*credentialCacheEntry), nil
	}
	if s.credentialCacheEntryCount >= maxCredentialCacheEntries {
		now := s.clock.Now()
		s.credentialCache.Range(func(key, value any) bool {
			entry := value.(*credentialCacheEntry)
			if !entry.mu.TryLock() {
				return true
			}
			defer entry.mu.Unlock()
			if entry.refresh != nil {
				return true
			}
			if entry.resp != nil && now.Before(entry.expiresAt) {
				return true
			}
			if !entry.lastFailedRefreshAt.IsZero() && now.Sub(entry.lastFailedRefreshAt) < staleRetryBackoff {
				return true
			}
			entry.evicted = true
			s.credentialCache.Delete(key)
			s.credentialCacheEntryCount--
			return false
		})
		if s.credentialCacheEntryCount >= maxCredentialCacheEntries {
			return nil, fmt.Errorf("credential cache full (%d entries max): too many distinct roleArns", maxCredentialCacheEntries)
		}
	}
	e := &credentialCacheEntry{}
	s.credentialCache.Store(roleARN, e)
	s.credentialCacheEntryCount++
	return e, nil
}
