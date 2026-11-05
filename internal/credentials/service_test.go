package credentials

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloud-idaas/idaas-go-core-sdk/factory"
)

func TestServiceStartupLogSanitizesEndpoint(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	factory.Reset()
	t.Cleanup(factory.Reset)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	// SDK prefetch fails on this missing OIDC file before it can make an HTTP request.
	configuration := `{
		"idaasInstanceId":"fixture", "clientId":"fixture",
		"issuer":"https://example.invalid", "tokenEndpoint":"https://example.invalid/token",
		"scope":"urn:cloud:idaas:pam|cloud_account_role:obtain_access_credential",
		"developerApiEndpoint":"https://user:fake-url-password@example.invalid/base?access_token=fake-url-query#fake-url-fragment",
		"authnConfiguration":{
			"identityType":"CLIENT", "authnMethod":"OIDC", "clientDeployEnvironment":"KUBERNETES",
			"applicationFederatedCredentialName":"fixture", "oidcTokenFilePath":"` + filepath.Join(dir, "missing-token") + `"
		},
		"httpConfiguration":{"connectTimeout":5000,"readTimeout":10000}
	}`
	if err := os.WriteFile(configPath, []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewServiceWithRefreshPolicy(configPath, time.Second, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.httpClient.CloseIdleConnections)
	if s.credentialRefreshMinValidity() != 2*time.Minute || s.credentialRefreshTimeout() != time.Second {
		t.Fatal("configured refresh policy was not applied")
	}
	if !strings.Contains(output.String(), "developerApiEndpoint=https://example.invalid/base") {
		t.Fatal("startup log lost the sanitized endpoint")
	}
	for _, secret := range []string{"user:", "fake-url-password", "fake-url-query", "fake-url-fragment", "access_token="} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("startup log contains URL credentials, query or fragment: %s", secret)
		}
	}
}
