module github.com/cloud-idaas/idaas-akless-sidecar

go 1.24.0

// Published Core SDK for bootstrap identity. No local replace or cloud SDK dependency.
require (
	github.com/cloud-idaas/idaas-go-core-sdk v0.1.0-beta.1
	github.com/golang-jwt/jwt/v4 v4.5.2
)

require github.com/google/uuid v1.6.0 // indirect
