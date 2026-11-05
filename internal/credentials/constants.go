package credentials

import "time"

// All supported vendors use the same Developer API route and role query.
const (
	developerAPIPathTemplate = "/v2/%s/cloudAccountRoles/_/actions/obtainAccessCredential"
	roleExternalIDQueryParam = "cloudAccountRoleExternalId"
)

// Refresh after at most 4/5 of the remaining lifetime at fetch completion.
const staleFraction = 5

// Backoff starts when a failed refresh completes.
const staleRetryBackoff = 10 * time.Second

// Bound retained roles, including failed lookups in backoff.
const maxCredentialCacheEntries = 64

// Budget for each shared token acquisition and Developer API refresh.
const defaultCredentialRefreshTimeout = 10 * time.Second

// Cached credentials need more remaining lifetime than this to avoid waiting
// for a refresh. Operators must align this with their SDK's refresh window.
const defaultCredentialRefreshMinValidity = 5 * time.Minute

// Developer API cloudAccountVendorType values.
const (
	vendorAlibaba = "alibaba_cloud"
	vendorAWS     = "aws"
	vendorTencent = "tencent_cloud"
)
