// Package credentials fetches and caches IDaaS-authorized cloud-role credentials
// and serves validated vendor contracts through a Pod-local HTTP handler.
package credentials

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/cloud-idaas/idaas-go-core-sdk/config"
	"github.com/cloud-idaas/idaas-go-core-sdk/factory"
	"github.com/cloud-idaas/idaas-go-core-sdk/provider"
	sdkutil "github.com/cloud-idaas/idaas-go-core-sdk/util"
)

// Service manages cloud credentials using a Core SDK bootstrap provider.
type Service struct {
	provider                  provider.IDaaSCredentialProvider
	developerAPIEndpoint      string
	idaasInstanceID           string
	refreshTimeout            time.Duration
	refreshMinValidity        time.Duration
	httpClient                *http.Client
	credentialCache           sync.Map   // roleARN -> *credentialCacheEntry
	credentialCacheMu         sync.Mutex // serializes insertion, eviction and entry count
	credentialCacheEntryCount int
	clock                     Clock
	fetchCredentials          func(ctx context.Context, roleARN string) (*developerAPIResponse, error)
}

// NewService initializes the credential service with the default refresh budget.
func NewService(configPath string) (*Service, error) {
	return NewServiceWithRefreshTimeout(configPath, defaultCredentialRefreshTimeout)
}

// NewServiceWithRefreshTimeout initializes bootstrap identity and the upstream budget.
func NewServiceWithRefreshTimeout(configPath string, timeout time.Duration) (*Service, error) {
	return NewServiceWithRefreshPolicy(configPath, timeout, defaultCredentialRefreshMinValidity)
}

// NewServiceWithRefreshPolicy configures the upstream budget and the minimum
// cached lifetime required to return immediately during a shared refresh.
func NewServiceWithRefreshPolicy(configPath string, timeout, minValidity time.Duration) (*Service, error) {
	if timeout <= 0 {
		return nil, errors.New("credential refresh timeout must be positive")
	}
	if minValidity <= 0 {
		return nil, errors.New("credential refresh minimum validity must be positive")
	}
	reader := config.NewConfigReader()
	cfg, err := reader.LoadWithPriority(configPath)
	if err != nil {
		return nil, fmt.Errorf("load IDaaS config (%s): %w", configPath, err)
	}
	if err := factory.InitWithConfig(cfg); err != nil {
		return nil, fmt.Errorf("initialize IDaaS Core SDK factory: %w", err)
	}

	// 空 scope 使用 SDK JSON 中的完整 scope。
	p, err := factory.GetIDaaSCredentialProviderByScope("")
	if err != nil {
		return nil, fmt.Errorf("get IDaaS credential provider: %w", err)
	}
	developerAPIEndpoint := factory.GetDeveloperApiEndpoint()
	idaasInstanceID := factory.GetIDaaSInstanceId()

	log.Printf("IDaaS Core SDK initialized: idaasInstanceId=%s developerApiEndpoint=%s authnMethod=%s clientDeployEnvironment=%s",
		cfg.InstanceId, sdkutil.SanitizeEndpointForLog(developerAPIEndpoint),
		cfg.AuthnConfiguration.AuthnMethod, cfg.AuthnConfiguration.ClientDeployEnvironment)
	if developerAPIEndpoint == "" || idaasInstanceID == "" {
		return nil, errors.New("Developer API endpoint or instance ID is empty in config")
	}
	s := &Service{
		provider:             p,
		developerAPIEndpoint: developerAPIEndpoint,
		idaasInstanceID:      idaasInstanceID,
		refreshTimeout:       timeout,
		refreshMinValidity:   minValidity,
		httpClient:           newDeveloperAPIClient(timeout),
		clock:                &SystemClock{},
	}
	s.fetchCredentials = s.callDeveloperAPI
	return s, nil
}

// Keep Developer API access tokens on the configured endpoint. This transport is
// independent of environment proxies and the Core SDK's own HTTP client.
func newDeveloperAPIClient(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: timeout,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// credentialRefreshTimeout returns the configured budget or the default.
func (s *Service) credentialRefreshTimeout() time.Duration {
	if s.refreshTimeout > 0 {
		return s.refreshTimeout
	}
	return defaultCredentialRefreshTimeout
}

func (s *Service) credentialRefreshMinValidity() time.Duration {
	if s.refreshMinValidity > 0 {
		return s.refreshMinValidity
	}
	return defaultCredentialRefreshMinValidity
}
