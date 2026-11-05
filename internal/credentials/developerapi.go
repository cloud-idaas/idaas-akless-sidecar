package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The shared worker logs refresh failures; waiters and backoff responses must not repeat them.
var errCredentialRefreshUnavailable = errors.New("credential refresh unavailable")

// serveDeveloperAPIResponse shares per-role refreshes. Cached credentials with
// sufficient remaining lifetime return immediately while a refresh runs.
// Cold and near-expiry callers may cancel without canceling the shared refresh.
func (s *Service) serveDeveloperAPIResponse(ctx context.Context, roleARN string) (*developerAPIResponse, error) {
	var entry *credentialCacheEntry
	var err error
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry, err = s.getOrCreateCredentialEntry(roleARN)
		if err != nil {
			return nil, err
		}
		entry.mu.Lock()
		if !entry.evicted {
			break
		}
		entry.mu.Unlock()
	}
	if err := ctx.Err(); err != nil {
		entry.mu.Unlock()
		return nil, err
	}
	now := s.clock.Now()
	cached := entry.resp
	canServeWhileRefreshing := cached != nil && entry.expiresAt.Sub(now) > s.credentialRefreshMinValidity()
	if call := entry.refresh; call != nil {
		entry.mu.Unlock()
		if canServeWhileRefreshing {
			return cached, nil
		}
		return waitForCredentialRefresh(ctx, call)
	}

	needRefresh := entry.resp == nil || !now.Before(entry.expiresAt) || (!entry.staleTime.IsZero() && !now.Before(entry.staleTime))
	if !needRefresh {
		resp := entry.resp
		entry.mu.Unlock()
		return resp, nil
	}

	// During backoff, return only an unexpired cached credential.
	if !entry.lastFailedRefreshAt.IsZero() && now.Sub(entry.lastFailedRefreshAt) < staleRetryBackoff {
		defer entry.mu.Unlock()
		if entry.resp != nil && now.Before(entry.expiresAt) {
			// The worker logs each failed refresh once; cache hits must not amplify it.
			return entry.resp, nil
		}
		return nil, fmt.Errorf("%w (in backoff, roleArn=%s)", errCredentialRefreshUnavailable, MaskARN(roleARN))
	}

	call := &credentialRefresh{done: make(chan struct{})}
	entry.refresh = call
	entry.mu.Unlock()
	go s.runCredentialRefresh(entry, roleARN, call)
	if canServeWhileRefreshing {
		return cached, nil
	}
	return waitForCredentialRefresh(ctx, call)
}

func waitForCredentialRefresh(ctx context.Context, call *credentialRefresh) (*developerAPIResponse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-call.done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return call.resp, call.err
	}
}

// Only one worker exists per retained role. The cache cannot evict an active worker.
func (s *Service) runCredentialRefresh(entry *credentialCacheEntry, roleARN string, call *credentialRefresh) {
	refreshCtx, cancel := context.WithTimeout(context.Background(), s.credentialRefreshTimeout())
	defer cancel()
	resp, err := s.fetchCredentialSafely(refreshCtx, roleARN)
	entry.mu.Lock()
	defer func() {
		entry.refresh = nil
		entry.mu.Unlock()
		close(call.done)
	}()
	now := s.clock.Now()
	if err == nil {
		// Cache only complete credentials that are still valid after the fetch.
		var exp time.Time
		exp, err = validateCredentialContent(resp)
		if err == nil {
			if !now.Before(exp) {
				err = fmt.Errorf("Developer API returned already-expired credential")
			} else {
				entry.resp = resp
				entry.expiresAt = exp
				// Use the lifetime left after IO, so a slow success is not stale on arrival.
				ttl := exp.Sub(now)
				entry.staleTime = exp.Add(-ttl / staleFraction)
				// Leave room for both the usable-cache margin and one refresh.
				// Short-lived credentials keep the proportional refresh point;
				// subtraction avoids duration overflow for large configured values.
				margin, budget := s.credentialRefreshMinValidity(), s.credentialRefreshTimeout()
				if margin < ttl && budget < ttl-margin {
					if earlier := exp.Add(-margin).Add(-budget); earlier.Before(entry.staleTime) {
						entry.staleTime = earlier
					}
				}
				entry.lastFailedRefreshAt = time.Time{}
				call.resp = resp
				return
			}
		}
	}
	// Fetch and validation failures share the same backoff and fallback.
	entry.lastFailedRefreshAt = now
	if entry.resp != nil && now.Before(entry.expiresAt) {
		log.Printf("credential refresh failed, serving stale still-valid (roleArn=%s): %v",
			MaskARN(roleARN), err)
		call.resp = entry.resp
		return
	}
	log.Printf("credential refresh failed (roleArn=%s): %v", MaskARN(roleARN), err)
	call.err = fmt.Errorf("%w (roleArn=%s): %w", errCredentialRefreshUnavailable, MaskARN(roleARN), err)
}

// A worker panic must not crash the process or leave waiters permanently blocked.
func (s *Service) fetchCredentialSafely(ctx context.Context, roleARN string) (resp *developerAPIResponse, err error) {
	defer func() {
		if recover() != nil {
			logRefreshPanic()
			resp, err = nil, errors.New("credential refresh panicked")
		}
	}()
	return s.fetchCredentials(ctx, roleARN)
}

// getAccessToken returns the Core SDK's Access Token for Developer API requests.
func (s *Service) getAccessToken(ctx context.Context) (string, error) {
	cred, err := s.provider.GetCredentialWithContext(ctx)
	if err != nil {
		return "", err
	}
	return cred.GetAccessToken(), nil
}

func (s *Service) developerAPIURL(roleARN string) string {
	path := fmt.Sprintf(developerAPIPathTemplate, s.idaasInstanceID)
	q := url.Values{}
	q.Set(roleExternalIDQueryParam, roleARN)
	return strings.TrimRight(s.developerAPIEndpoint, "/") + path + "?" + q.Encode()
}

func (s *Service) callDeveloperAPI(ctx context.Context, roleARN string) (*developerAPIResponse, error) {
	accessToken, err := s.getAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("obtain IDaaS access token: %w", err)
	}
	reqURL := s.developerAPIURL(roleARN)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build Developer API request: %w", withoutRequestURL(err))
	}
	if req.URL.Scheme != "https" || req.URL.Hostname() == "" || req.URL.User != nil || req.URL.Fragment != "" {
		return nil, errors.New("Developer API endpoint must use HTTPS without embedded credentials or fragment")
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call Developer API: %w", withoutRequestURL(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		// 不记录或回传上游错误体，避免凭据与内部信息泄漏。
		return nil, fmt.Errorf("Developer API returned status %d", resp.StatusCode)
	}
	const maxResponseBytes = 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Developer API response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return nil, errors.New("Developer API response exceeds size limit")
	}

	var devResp developerAPIResponse
	if err := json.Unmarshal(body, &devResp); err != nil {
		return nil, fmt.Errorf("unmarshal Developer API response: %w", err)
	}
	return &devResp, nil
}

// withoutRequestURL removes role-bearing URLs while retaining the error cause.
func withoutRequestURL(err error) error {
	var requestErr *url.Error
	for errors.As(err, &requestErr) {
		err = requestErr.Err
	}
	return err
}
