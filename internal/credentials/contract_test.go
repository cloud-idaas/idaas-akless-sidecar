package credentials

import (
	"encoding/json"
	"testing"
	"time"
)

func defaultCloudFixtures() []struct{ vendor, payload, want string } {
	return []struct{ vendor, payload, want string }{
		{vendorAlibaba, `"alibabaCloudStsToken":{"accessKeyId":"FAKE_ID","accessKeySecret":"FAKE_SECRET","securityToken":"FAKE_TOKEN","expiration":"2030-01-01T08:10:00+08:00"}`, `{"Code":"Success","AccessKeyId":"FAKE_ID","AccessKeySecret":"FAKE_SECRET","SecurityToken":"FAKE_TOKEN","Expiration":"2030-01-01T00:10:00Z"}`},
		{vendorAWS, `"awsStsToken":{"accessKeyId":"FAKE_ID","secretAccessKey":"FAKE_SECRET","sessionToken":"FAKE_TOKEN","expiration":"2030-01-01T00:10:00.123Z"}`, `{"AccessKeyId":"FAKE_ID","SecretAccessKey":"FAKE_SECRET","Token":"FAKE_TOKEN","Expiration":"2030-01-01T00:10:00Z"}`},
		{vendorTencent, `"tencentCloudStsToken":{"tmpSecretId":"FAKE_ID","tmpSecretKey":"FAKE_SECRET","token":"FAKE_TOKEN","expiration":"2030-01-01T00:10:00Z","expiredTime":1893456600}`, `{"tmpSecretId":"FAKE_ID","tmpSecretKey":"FAKE_SECRET","token":"FAKE_TOKEN","expiration":"2030-01-01T00:10:00Z","expiredTime":1893456600}`},
	}
}

func TestConvertToCloudContract(t *testing.T) {
	for _, tc := range defaultCloudFixtures() {
		t.Run(tc.vendor, func(t *testing.T) {
			got, err := convertToCloudContract(decodeFixture(t, tc.vendor, tc.payload))
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			// Exact JSON key sets reject undeclared aliases and protocol wrappers.
			assertJSON(t, body, tc.want)
		})
	}
}

func TestCredentialValidationRejectsMissingFields(t *testing.T) {
	for _, tc := range defaultCloudFixtures() {
		var source map[string]map[string]any
		if err := json.Unmarshal([]byte("{"+tc.payload+"}"), &source); err != nil {
			t.Fatal(err)
		}
		for dto, fields := range source {
			for field := range fields {
				t.Run(tc.vendor+"/"+field, func(t *testing.T) {
					saved := fields[field]
					delete(fields, field)
					body, _ := json.Marshal(source)
					fields[field] = saved
					resp := decodeFixture(t, tc.vendor, string(body[1:len(body)-1]))
					if _, err := convertToCloudContract(resp); err == nil {
						t.Fatal("missing DTO field accepted")
					}
				})
			}
			t.Run(tc.vendor+"/null", func(t *testing.T) {
				if _, err := convertToCloudContract(decodeFixture(t, tc.vendor, `"`+dto+`":null`)); err == nil {
					t.Fatal("null DTO accepted")
				}
			})
		}
	}
}

func TestCredentialValidationExpiryConsistency(t *testing.T) {
	for _, tc := range defaultCloudFixtures() {
		t.Run(tc.vendor, func(t *testing.T) {
			resp := decodeFixture(t, tc.vendor, tc.payload)
			resp.CloudAccountRoleAccessCredential.AccessCredentialExpiresAt++
			if _, err := validateCredentialContent(resp); err == nil {
				t.Fatal("mismatched expiry accepted")
			}
		})
	}
	for _, value := range []string{"not-a-date", "2030-01-01T00:00:00", "2000-01-01T00:00:00Z"} {
		resp := respWithExpiry(vendorAlibaba, 1893456600)
		resp.CloudAccountRoleAccessCredential.AlibabaCloudSTSToken.Expiration = value
		if _, err := validateCredentialContent(resp); err == nil {
			t.Fatal("invalid expiry accepted")
		}
	}
	resp := respWithExpiry(vendorTencent, 1893456600)
	resp.CloudAccountRoleAccessCredential.TencentCloudSTSToken.Token = "  "
	if _, err := responseForClient(resp, time.Unix(1893456000, 0)); err == nil {
		t.Fatal("blank token accepted")
	}
	for _, resp := range []*developerAPIResponse{nil, {}, {CloudAccountRoleAccessCredential: &accessCredentialEnvelope{}}} {
		if _, err := convertToCloudContract(resp); err == nil {
			t.Fatal("missing envelope accepted")
		}
	}
}
