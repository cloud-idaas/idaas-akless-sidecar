package credentials

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCredentialsHandlerDefaultAndRawContracts(t *testing.T) {
	for _, tc := range defaultCloudFixtures() {
		t.Run(tc.vendor, func(t *testing.T) {
			resp := decodeFixture(t, tc.vendor, tc.payload)
			metadata := `"cloudAccountId":"account","cloudAccountRoleId":"role","cloudAccountRoleName":"name","cloudAccountRoleExternalId":"external/id+符号",`
			if err := json.Unmarshal([]byte("{"+strings.TrimSuffix(metadata, ",")+"}"), resp); err != nil {
				t.Fatal(err)
			}
			fetch := &fakeFetch{resp: resp}
			s := newTestService(&fakeClock{now: time.Unix(1893456000, 0)}, fetch)
			h := s.CredentialsHandler("opaque-id", nil, testRequestAuthenticator{})
			normal := do(t, h, http.MethodGet, "/credentials")
			if normal.Code != http.StatusOK {
				t.Fatalf("default status %d: %s", normal.Code, normal.Body)
			}
			assertJSON(t, normal.Body.Bytes(), tc.want)
			// Optional bridge to real SDK probes; only fake fixture credentials are exported.
			if dir := os.Getenv("IDAAS_AKLESS_TEST_RESPONSE_DIR"); dir != "" {
				if err := os.WriteFile(filepath.Join(dir, tc.vendor+".json"), normal.Body.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
			}
			raw := do(t, h, http.MethodGet, "/credentials?format=raw")
			if raw.Code != http.StatusOK {
				t.Fatalf("raw status %d: %s", raw.Code, raw.Body)
			}
			var decoded developerAPIResponse
			if err := json.Unmarshal(raw.Body.Bytes(), &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.CloudAccountID == nil || *decoded.CloudAccountID != "account" ||
				decoded.CloudAccountRoleID == nil || *decoded.CloudAccountRoleID != "role" ||
				decoded.CloudAccountRoleName == nil || *decoded.CloudAccountRoleName != "name" ||
				decoded.CloudAccountRoleExternalID == nil || *decoded.CloudAccountRoleExternalID != "external/id+符号" {
				t.Fatal("raw lost account or role metadata")
			}
			want, _ := json.Marshal(resp)
			assertJSON(t, raw.Body.Bytes(), string(want))
			if fetch.count() != 1 {
				t.Fatal("formats must share one credential cache")
			}
		})
	}
}

func TestCredentialsHandlerRejectsInvalidFormatBeforeFetch(t *testing.T) {
	for _, query := range []string{"format", "format=", "format=RAW", "format=unknown", "format=raw&format=raw", "format=raw&format=", "format=%zz", "format=raw;bad=1", "roleArn=a&roleArn=b"} {
		t.Run(query, func(t *testing.T) {
			fetch := &fakeFetch{resp: respWithExpiry(vendorAWS, 1893456600)}
			s := newTestService(&fakeClock{now: time.Unix(1893456000, 0)}, fetch)
			rr := do(t, s.CredentialsHandler("opaque-id", nil, testRequestAuthenticator{}), http.MethodGet, "/credentials?"+query)
			if rr.Code != http.StatusBadRequest || fetch.count() != 0 {
				t.Fatalf("status=%d fetches=%d", rr.Code, fetch.count())
			}
			assertErrorResponse(t, rr.Header(), rr.Body.Bytes())
		})
	}
}

func assertErrorResponse(t *testing.T, header http.Header, body []byte) {
	t.Helper()
	if header.Get("Cache-Control") != "no-store" || header.Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatal("missing error JSON/no-store headers")
	}
	var result struct{ Code, Message string }
	if json.Unmarshal(body, &result) != nil || result.Code == "" || result.Message == "" {
		t.Fatal("missing error code/message")
	}
	if strings.Contains(string(body), "fake-secret") || strings.Contains(string(body), "fake-token") {
		t.Fatal("error leaked a credential")
	}
}

func TestBothFormatsRejectIncompleteCredentialsWithoutCaching(t *testing.T) {
	for _, tc := range defaultCloudFixtures() {
		for _, suffix := range []string{"", "?format=raw"} {
			t.Run(tc.vendor+suffix, func(t *testing.T) {
				// Keep a valid outer expiry but remove the credential's token.
				payload := strings.ReplaceAll(tc.payload, `"FAKE_TOKEN"`, `""`)
				payload = strings.ReplaceAll(payload, `"fake-token"`, `""`)
				resp := decodeFixture(t, tc.vendor, payload)
				s := newTestService(&fakeClock{now: time.Unix(1893456000, 0)}, &fakeFetch{resp: resp})
				rr := do(t, s.CredentialsHandler("opaque-id", nil, testRequestAuthenticator{}), http.MethodGet, "/credentials"+suffix)
				if rr.Code != http.StatusBadGateway {
					t.Fatalf("incomplete credential accepted: %d", rr.Code)
				}
				assertErrorResponse(t, rr.Header(), rr.Body.Bytes())
				entry, _ := s.getOrCreateCredentialEntry("opaque-id")
				if entry.resp != nil {
					t.Fatal("incomplete credential cached")
				}
			})
		}
	}
}

func TestInvalidRefreshRetainsValidCachedCredential(t *testing.T) {
	clk := &fakeClock{now: time.Unix(1893456000, 0)}
	valid := respWithExpiry(vendorAWS, clk.now.Add(1000*time.Second).Unix())
	fetch := &fakeFetch{resp: valid}
	s := newTestService(clk, fetch)
	if _, err := s.serveDeveloperAPIResponse(context.Background(), "opaque-id"); err != nil {
		t.Fatal(err)
	}
	invalid := respWithExpiry(vendorAWS, clk.now.Add(2000*time.Second).Unix())
	invalid.CloudAccountRoleAccessCredential.AWSSTSToken.SessionToken = ""
	fetch.set(invalid, nil)
	clk.now = clk.now.Add(850 * time.Second)
	got, err := s.serveDeveloperAPIResponse(context.Background(), "opaque-id")
	if err != nil || got != valid {
		t.Fatal("invalid refresh replaced usable cached credential")
	}
}

func TestNullableDeveloperAPIFieldsRemainNull(t *testing.T) {
	var resp developerAPIResponse
	if err := json.Unmarshal([]byte(`{"cloudAccountId":null,"cloudAccountRoleAccessCredential":{"tencentCloudStsToken":{"expiredTime":null}}}`), &resp); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(resp)
	if !strings.Contains(string(body), `"cloudAccountId":null`) || !strings.Contains(string(body), `"expiredTime":null`) {
		t.Fatal("null was silently converted to a zero value")
	}
}
