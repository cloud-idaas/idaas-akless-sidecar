package credentials

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// convertToCloudContract maps validated credentials to the vendor's default format.
func convertToCloudContract(resp *developerAPIResponse) (any, error) {
	expiry, err := validateCredentialContent(resp)
	if err != nil {
		return nil, err
	}
	ac := resp.CloudAccountRoleAccessCredential
	switch resp.CloudAccountVendorType {
	case vendorAlibaba:
		sts := ac.AlibabaCloudSTSToken
		return alibabaCredentialsContract{
			Code:            "Success",
			AccessKeyID:     sts.AccessKeyID,
			AccessKeySecret: sts.AccessKeySecret,
			SecurityToken:   sts.SecurityToken,
			Expiration:      expiry.UTC().Format(time.RFC3339),
		}, nil
	case vendorAWS:
		sts := ac.AWSSTSToken
		return awsContainerCredentialsContract{
			AccessKeyID:     sts.AccessKeyID,
			SecretAccessKey: sts.SecretAccessKey,
			Token:           sts.SessionToken,
			Expiration:      expiry.UTC().Format(time.RFC3339),
		}, nil
	case vendorTencent:
		return *ac.TencentCloudSTSToken, nil
	default:
		return nil, fmt.Errorf("unsupported cloudAccountVendorType: %q", resp.CloudAccountVendorType)
	}
}

// validateCredentialContent checks required fields and matching Unix-second expiries.
// Callers separately reject credentials expired against their clock.
func validateCredentialContent(resp *developerAPIResponse) (time.Time, error) {
	if resp == nil || resp.CloudAccountRoleAccessCredential == nil {
		return time.Time{}, errors.New("missing cloudAccountRoleAccessCredential")
	}
	ac := resp.CloudAccountRoleAccessCredential
	if ac.AccessCredentialExpiresAt <= 0 {
		return time.Time{}, errors.New("missing accessCredentialExpiresAt")
	}
	var expiration string
	switch resp.CloudAccountVendorType {
	case vendorAlibaba:
		c := ac.AlibabaCloudSTSToken
		if c == nil || !nonempty(c.AccessKeyID, c.AccessKeySecret, c.SecurityToken, c.Expiration) {
			return time.Time{}, errors.New("missing or invalid alibabaCloudStsToken")
		}
		expiration = c.Expiration
	case vendorAWS:
		c := ac.AWSSTSToken
		if c == nil || !nonempty(c.AccessKeyID, c.SecretAccessKey, c.SessionToken, c.Expiration) {
			return time.Time{}, errors.New("missing or invalid awsStsToken")
		}
		expiration = c.Expiration
	case vendorTencent:
		c := ac.TencentCloudSTSToken
		if c == nil || !nonempty(c.TmpSecretID, c.TmpSecretKey, c.Token, c.Expiration) ||
			c.ExpiredTime == nil || *c.ExpiredTime != ac.AccessCredentialExpiresAt {
			return time.Time{}, errors.New("missing or invalid tencentCloudStsToken")
		}
		expiration = c.Expiration
	default:
		return time.Time{}, fmt.Errorf("unsupported cloudAccountVendorType: %q", resp.CloudAccountVendorType)
	}
	expiry := time.Unix(ac.AccessCredentialExpiresAt, 0)
	if expiration != "" {
		parsed, err := time.Parse(time.RFC3339Nano, expiration)
		if err != nil || parsed.Unix() != ac.AccessCredentialExpiresAt {
			return time.Time{}, errors.New("invalid or inconsistent credential expiration")
		}
	}
	return expiry, nil
}

func nonempty(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}

// responseForClient applies the same content and expiry checks to default and raw output.
func responseForClient(resp *developerAPIResponse, now time.Time) (*developerAPIResponse, error) {
	expiry, err := validateCredentialContent(resp)
	if err != nil {
		return nil, err
	}
	if !now.Before(expiry) {
		return nil, errors.New("cloud credential has expired")
	}
	return resp, nil
}
