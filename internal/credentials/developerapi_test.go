package credentials

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	sdkcredential "github.com/cloud-idaas/idaas-go-core-sdk/credential"
	sdkprovider "github.com/cloud-idaas/idaas-go-core-sdk/provider"
)

type fakeProvider struct {
	sdkprovider.IDaaSCredentialProvider
	err error
}

func (p fakeProvider) GetCredentialWithContext(context.Context) (sdkcredential.IDaaSCredential, error) {
	return fakeAccessToken{}, p.err
}

type fakeAccessToken struct{ sdkcredential.IDaaSCredential }

func (fakeAccessToken) GetAccessToken() string { return "fake-bootstrap-token" }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (failingBody) Close() error             { return nil }

func TestDeveloperAPIRequestAndDTOParsing(t *testing.T) {
	for _, tc := range defaultCloudFixtures() {
		t.Run(tc.vendor, func(t *testing.T) {
			s := &Service{provider: fakeProvider{}, developerAPIEndpoint: "https://example.invalid/", idaasInstanceID: "idaas_test"}
			s.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Query().Get("cloudAccountRoleExternalId") != "external/id+with:symbols" {
					t.Fatal("external ID not preserved")
				}
				if req.URL.Path != "/v2/idaas_test/cloudAccountRoles/_/actions/obtainAccessCredential" || req.Method != http.MethodGet {
					t.Fatal("unexpected Developer API route")
				}
				if req.Header.Get("Authorization") != "Bearer fake-bootstrap-token" {
					t.Fatal("missing bootstrap bearer token")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"cloudAccountVendorType":"` + tc.vendor + `","cloudAccountRoleAccessCredential":{"accessCredentialExpiresAt":1893456600,` + tc.payload + `}}`))}, nil
			})}
			resp, err := s.callDeveloperAPI(context.Background(), "external/id+with:symbols")
			if err != nil {
				t.Fatal(err)
			}
			contract, err := convertToCloudContract(resp)
			if err != nil || contract == nil {
				t.Fatalf("DTO not usable: %v", err)
			}
		})
	}
}

func TestDeveloperAPIFailures(t *testing.T) {
	for _, tc := range []struct {
		name           string
		providerError  error
		endpoint       string
		status         int
		body           io.ReadCloser
		transportError error
	}{
		{name: "bootstrap unavailable", providerError: errors.New("bootstrap unavailable")},
		{name: "invalid URL", endpoint: "://invalid"},
		{name: "transport failure", transportError: errors.New("transport failure")},
		{name: "response read failure", status: 200, body: failingBody{}},
		{name: "non-200", status: 403, body: io.NopCloser(strings.NewReader("fake-sensitive-upstream-detail"))},
		{name: "invalid JSON", status: 200, body: io.NopCloser(strings.NewReader("invalid"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := tc.endpoint
			if endpoint == "" {
				endpoint = "https://example.invalid"
			}
			s := &Service{provider: fakeProvider{err: tc.providerError}, developerAPIEndpoint: endpoint, idaasInstanceID: "test", httpClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				if tc.transportError != nil {
					return nil, tc.transportError
				}
				return &http.Response{StatusCode: tc.status, Body: tc.body}, nil
			})}}
			_, err := s.callDeveloperAPI(context.Background(), "opaque-id")
			if err == nil {
				t.Fatal("expected upstream failure")
			}
			if strings.Contains(err.Error(), "fake-sensitive-upstream-detail") {
				t.Fatal("upstream body leaked into returned error")
			}
		})
	}
}

func TestCredentialLogMasking(t *testing.T) {
	for value, want := range map[string]string{"": "(none)", "acs:ram::123:role/demo": "acs:ram::********:role/demo", "arn:aws:iam::123:role/demo": "arn:aws:iam::********:role/demo", "qcs::cam::uin/123:roleName/demo": "qcs::cam::uin/********:roleName/demo", "opaque-id": "(redacted)", "qcs::cam::uin/malformed": "(redacted)", "acs:ram::malformed": "(redacted)"} {
		if got := MaskARN(value); got != want {
			t.Fatalf("mask %q = %q, want %q", value, got, want)
		}
	}
}

func TestTransportFailureDoesNotLogRoleURL(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprintf("cached=%v", cached), func(t *testing.T) {
			const role = "acs:ram::123456789012:role/demo"
			clk := &fakeClock{now: time.Now()}
			cause := errors.New("dial failed")
			s := newTestService(clk, &fakeFetch{})
			s.provider = fakeProvider{}
			s.developerAPIEndpoint = "https://example.invalid"
			s.idaasInstanceID = "test"
			s.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, &url.Error{Op: "transport", URL: s.developerAPIURL(role), Err: cause}
			})}
			s.fetchCredentials = s.callDeveloperAPI
			_, err := s.callDeveloperAPI(context.Background(), role)
			if !errors.Is(err, cause) || strings.Contains(err.Error(), "123456789012") {
				t.Fatalf("transport cause lost or request URL leaked: %v", err)
			}
			if cached {
				exp := clk.now.Add(time.Hour)
				seedEntry(s, role, respWithExpiry(vendorAlibaba, exp.Unix()), exp, clk.now.Add(-time.Second), time.Time{})
			}
			var logs bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(previous)
			rr := do(t, s.CredentialsHandler(role, nil, testRequestAuthenticator{}), http.MethodGet, "/credentials")
			waitForTestRefresh(t, s, role)
			wantStatus := http.StatusBadGateway
			if cached {
				wantStatus = http.StatusOK
			}
			if rr.Code != wantStatus || !strings.Contains(logs.String(), "dial failed") {
				t.Fatalf("missing sanitized failure log: status=%d log=%s", rr.Code, &logs)
			}
			if strings.Contains(logs.String(), "123456789012") || strings.Contains(logs.String(), "cloudAccountRoleExternalId") {
				t.Fatalf("request URL leaked to log: %s", &logs)
			}
		})
	}
}
