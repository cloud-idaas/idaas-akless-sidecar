package credentials

import "net/http"

// UnauthenticatedAccess is selected only by an explicit startup configuration.
// It bypasses local token checks; role restrictions and upstream IDaaS identity
// remain enforced by the normal handler and service. A nil authenticator still fails closed.
type UnauthenticatedAccess struct{}

func (UnauthenticatedAccess) Authenticate(*http.Request) (func() error, error) {
	return func() error { return nil }, nil
}
