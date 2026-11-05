package credentials

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCredentialRefreshBudget(t *testing.T) {
	for _, timeout := range []time.Duration{0, 25 * time.Second} {
		s := newTestService(&fakeClock{now: time.Now()}, &fakeFetch{})
		s.refreshTimeout = timeout
		expected := timeout
		if expected == 0 {
			expected = defaultCredentialRefreshTimeout
		}
		s.fetchCredentials = func(ctx context.Context, _ string) (*developerAPIResponse, error) {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Error("refresh must have a deadline")
				return nil, errors.New("missing refresh deadline")
			}
			remaining := time.Until(deadline)
			if remaining <= 0 || remaining > expected || remaining < expected-time.Second {
				t.Errorf("remaining=%v, expected=%v", remaining, expected)
			}
			return nil, errors.New("test upstream failure")
		}
		if _, err := s.serveDeveloperAPIResponse(context.Background(), testRoleARN); err == nil {
			t.Fatal("expected upstream error")
		}
	}
}

func TestInvalidRefreshBudgetRejectedBeforeSDKInitialization(t *testing.T) {
	for _, timeout := range []time.Duration{0, -time.Second} {
		if _, err := NewServiceWithRefreshTimeout("/does-not-exist", timeout); err == nil || err.Error() != "credential refresh timeout must be positive" {
			t.Fatalf("unexpected validation: %v", err)
		}
	}
}

func TestRefreshMinimumValidityPolicy(t *testing.T) {
	for _, invalid := range []time.Duration{0, -time.Second} {
		if _, err := NewServiceWithRefreshPolicy("/does-not-exist", time.Second, invalid); err == nil || err.Error() != "credential refresh minimum validity must be positive" {
			t.Fatalf("unexpected minimum validity validation: %v", err)
		}
	}
	s := &Service{}
	if s.credentialRefreshMinValidity() != 5*time.Minute {
		t.Fatal("missing default minimum validity")
	}
	s.refreshMinValidity = 2 * time.Minute
	if s.credentialRefreshMinValidity() != 2*time.Minute {
		t.Fatal("configured minimum validity ignored")
	}
}
