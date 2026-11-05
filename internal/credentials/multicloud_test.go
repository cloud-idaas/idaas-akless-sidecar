package credentials

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Unsupported vendor payloads must fail even when raw output is requested.
func unsupportedCloudFixtures() []struct{ vendor, payload string } {
	return []struct{ vendor, payload string }{
		{"entra_id", `"entraIdAccessToken":{"accessToken":"fake-token","expiresIn":600}`},
		{"huawei_cloud", `"huaweiCloudStsToken":{"access":"fake-id","secret":"fake-secret","securitytoken":"fake-token","expiresAt":"2030-01-01T00:10:00Z"}`},
		{"gcp", `"gcpAccessToken":{"accessToken":"fake-token","expireTime":"2030-01-01T00:10:00Z"}`},
		{"azure", `"entraIdAccessToken":{"accessToken":"fake-token","expiresIn":600}`},
		{"unknown", `"alibabaCloudStsToken":{"accessKeyId":"fake-id","accessKeySecret":"fake-secret","securityToken":"fake-token","expiration":"2030-01-01T00:10:00Z"}`},
	}
}

func decodeFixture(t *testing.T, vendor, payload string) *developerAPIResponse {
	t.Helper()
	body := `{"cloudAccountVendorType":"` + vendor + `","cloudAccountRoleAccessCredential":{"accessCredentialExpiresAt":1893456600,` + payload + `}}`
	var resp developerAPIResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	return &resp
}

func assertJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var actual, expected any
	if err := json.Unmarshal(got, &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("JSON mismatch: got %s, want %s", got, want)
	}
}

func TestUnsupportedCloudsRejectBothFormatsWithoutCaching(t *testing.T) {
	for _, tc := range unsupportedCloudFixtures() {
		for _, suffix := range []string{"", "?format=raw"} {
			t.Run(tc.vendor+suffix, func(t *testing.T) {
				resp := decodeFixture(t, tc.vendor, tc.payload)
				if _, err := convertToCloudContract(resp); err == nil {
					t.Fatal("unsupported vendor converted")
				}
				if _, err := responseForClient(resp, time.Unix(1893456000, 0)); err == nil {
					t.Fatal("unsupported raw response accepted")
				}
				s := newTestService(&fakeClock{now: time.Unix(1893456000, 0)}, &fakeFetch{resp: resp})
				rr := do(t, s.CredentialsHandler("opaque-id", nil, testRequestAuthenticator{}), http.MethodGet, "/credentials"+suffix)
				if rr.Code != http.StatusBadGateway {
					t.Fatalf("unsupported vendor returned %d: %s", rr.Code, rr.Body)
				}
				assertErrorResponse(t, rr.Header(), rr.Body.Bytes())
				assertJSON(t, rr.Body.Bytes(), `{"code":"CredentialUnavailable","message":"failed to obtain credential"}`)
				entry, _ := s.getOrCreateCredentialEntry("opaque-id")
				if entry.resp != nil {
					t.Fatal("unsupported credential cached")
				}
			})
		}
	}
}

func TestUnsupportedRefreshRetainsOnlyUnexpiredSupportedCredential(t *testing.T) {
	for _, tc := range unsupportedCloudFixtures() {
		t.Run(tc.vendor, func(t *testing.T) {
			clk := &fakeClock{now: time.Unix(1893456000, 0)}
			valid := respWithExpiry(vendorTencent, clk.now.Add(1000*time.Second).Unix())
			fetch := &fakeFetch{resp: valid}
			s := newTestService(clk, fetch)
			if _, err := s.serveDeveloperAPIResponse(context.Background(), "opaque-id"); err != nil {
				t.Fatal(err)
			}
			fetch.set(decodeFixture(t, tc.vendor, tc.payload), nil)
			clk.now = clk.now.Add(850 * time.Second)
			got, err := s.serveDeveloperAPIResponse(context.Background(), "opaque-id")
			if err != nil || got != valid {
				t.Fatal("unsupported refresh replaced valid cached credential")
			}
			clk.now = time.Unix(1893457000, 0)
			if _, err := s.serveDeveloperAPIResponse(context.Background(), "opaque-id"); err == nil {
				t.Fatal("expired credential served after unsupported refresh")
			}
		})
	}
}

func TestResponseForClientSupportedCloudExpiry(t *testing.T) {
	now := time.Unix(1893456000, 0)
	for _, resp := range []*developerAPIResponse{nil, {}, {CloudAccountRoleAccessCredential: &accessCredentialEnvelope{}}} {
		if _, err := responseForClient(resp, now); err == nil {
			t.Fatal("missing response accepted")
		}
	}
	for _, tc := range defaultCloudFixtures() {
		t.Run(tc.vendor, func(t *testing.T) {
			resp := decodeFixture(t, tc.vendor, tc.payload)
			before, _ := json.Marshal(resp)
			got, err := responseForClient(resp, time.Unix(1893456599, 500000000))
			if err != nil || got != resp {
				t.Fatal("unexpired STS credential rejected")
			}
			after, _ := json.Marshal(resp)
			if string(before) != string(after) {
				t.Fatal("cached credential mutated")
			}
			for _, instant := range []time.Time{time.Unix(1893456600, 0), time.Unix(1893456601, 0)} {
				if _, err := responseForClient(resp, instant); err == nil {
					t.Fatal("expired credential accepted")
				}
			}
		})
	}
}

func TestRoleIdentifierValidationAndForwarding(t *testing.T) {
	for _, value := range []string{"acs:ram::123:role/r", "arn:aws:iam::123:role/r", "qcs::cam::uin/123:roleName/r", "uuid-opaque", "group/path", "principal://example/id", strings.Repeat("a", 2048)} {
		if !validRoleIdentifier(value) {
			t.Fatalf("valid external ID rejected: %q", value)
		}
		s := &Service{idaasInstanceID: "instance", developerAPIEndpoint: "https://example.invalid"}
		u, err := url.Parse(s.developerAPIURL(value))
		if err != nil || u.Query().Get("cloudAccountRoleExternalId") != value {
			t.Fatal("external ID changed during forwarding")
		}
	}
	for _, value := range []string{"", "white space", "a\n", "a\x00", "a\u2003b", string([]byte{0xff}), strings.Repeat("a", 2049)} {
		if validRoleIdentifier(value) {
			t.Fatalf("invalid external ID accepted: %q", value)
		}
	}
	if got := MaskARN("opaque-secret-identifier"); got != "(redacted)" {
		t.Fatalf("opaque role ID leaked: %s", got)
	}
}

func TestRefreshRejectsCredentialsExpiredDuringFetch(t *testing.T) {
	clk := &fakeClock{now: time.Unix(1893456000, 0)}
	s := &Service{clock: clk}
	s.fetchCredentials = func(context.Context, string) (*developerAPIResponse, error) {
		resp := respWithExpiry(vendorAWS, clk.now.Add(time.Second).Unix())
		clk.now = clk.now.Add(2 * time.Second)
		return resp, nil
	}
	if _, err := s.serveDeveloperAPIResponse(context.Background(), "opaque-id"); err == nil {
		t.Fatal("expired in-flight response accepted")
	}
	s2 := &Service{clock: clk}
	s2.fetchCredentials = func(context.Context, string) (*developerAPIResponse, error) { return nil, nil }
	if _, err := s2.serveDeveloperAPIResponse(context.Background(), "opaque-id"); err == nil {
		t.Fatal("nil response accepted")
	}
}

func TestSupportedCloudContractsRejectMissingPayload(t *testing.T) {
	for _, vendor := range []string{vendorAlibaba, vendorAWS, vendorTencent} {
		if _, err := convertToCloudContract(&developerAPIResponse{CloudAccountVendorType: vendor, CloudAccountRoleAccessCredential: &accessCredentialEnvelope{}}); err == nil {
			t.Fatalf("missing %s payload accepted", vendor)
		}
	}
}
