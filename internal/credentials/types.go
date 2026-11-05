package credentials

// developerAPIResponse declares the fields retained in raw output.
type developerAPIResponse struct {
	CloudAccountID                   *string                   `json:"cloudAccountId"`
	CloudAccountRoleID               *string                   `json:"cloudAccountRoleId"`
	CloudAccountRoleName             *string                   `json:"cloudAccountRoleName"`
	CloudAccountRoleExternalID       *string                   `json:"cloudAccountRoleExternalId"`
	CloudAccountVendorType           string                    `json:"cloudAccountVendorType"` // top-level, self-describes the cloud vendor
	CloudAccountRoleAccessCredential *accessCredentialEnvelope `json:"cloudAccountRoleAccessCredential"`
}

type accessCredentialEnvelope struct {
	AccessCredentialExpiresAt int64               `json:"accessCredentialExpiresAt"` // unix seconds, absolute expiry for all cloud credentials
	AlibabaCloudSTSToken      *alibabaSTSEnvelope `json:"alibabaCloudStsToken"`
	AWSSTSToken               *awsSTSEnvelope     `json:"awsStsToken"`
	TencentCloudSTSToken      *tencentSTSEnvelope `json:"tencentCloudStsToken"`
}

type alibabaSTSEnvelope struct {
	AccessKeyID     string `json:"accessKeyId"`
	AccessKeySecret string `json:"accessKeySecret"`
	SecurityToken   string `json:"securityToken"`
	Expiration      string `json:"expiration"` // RFC3339; validated at Unix-second precision.
}

type awsSTSEnvelope struct {
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	SessionToken    string `json:"sessionToken"`
	Expiration      string `json:"expiration"`
}

// tencentSTSEnvelope declares the supported Tencent credential DTO fields.
type tencentSTSEnvelope struct {
	TmpSecretID  string `json:"tmpSecretId"`
	TmpSecretKey string `json:"tmpSecretKey"`
	Token        string `json:"token"`
	Expiration   string `json:"expiration"`
	ExpiredTime  *int64 `json:"expiredTime"` // Unix seconds; nil detects missing/null values, which validation rejects.
}

// alibabaCredentialsContract maps the Alibaba Cloud Credentials URI response
// fields. Local authentication support depends on the consuming SDK or adapter.
type alibabaCredentialsContract struct {
	Code            string `json:"Code"` // fixed "Success"
	AccessKeyID     string `json:"AccessKeyId"`
	AccessKeySecret string `json:"AccessKeySecret"`
	SecurityToken   string `json:"SecurityToken"`
	Expiration      string `json:"Expiration"`
}

// awsContainerCredentialsContract is consumed via AWS_CONTAINER_CREDENTIALS_FULL_URI.
type awsContainerCredentialsContract struct {
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	Token           string `json:"Token"`
	Expiration      string `json:"Expiration"`
}
