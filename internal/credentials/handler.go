package credentials

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"

	"github.com/cloud-idaas/idaas-akless-sidecar/internal/projectedauth"
)

// RequestAuthenticator validates before cache access and returns a final check
// to run before releasing STS. An error or a nil check must deny the request.
type RequestAuthenticator interface {
	Authenticate(*http.Request) (func() error, error)
}

// CredentialsHandler serves the vendor default or format=raw. Role restrictions
// are optional; IDaaS authorizes this Sidecar's application identity upstream.
func (s *Service) CredentialsHandler(defaultRoleARN string, allowedARNs map[string]struct{}, auth RequestAuthenticator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		written := false
		defer func() {
			if recover() != nil {
				log.Print("credentials handler panic")
				if !written {
					writeCredentialError(w, http.StatusInternalServerError, "InternalError", "internal error")
				}
			}
		}()
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeCredentialError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
			return
		}
		if auth == nil {
			writeAuthenticationError(w, projectedauth.ErrUnavailable)
			return
		}
		recheck, err := auth.Authenticate(r)
		if err != nil {
			writeAuthenticationError(w, err)
			return
		}
		if recheck == nil {
			writeAuthenticationError(w, projectedauth.ErrUnavailable)
			return
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(query["roleArn"]) > 1 {
			writeCredentialError(w, http.StatusBadRequest, "InvalidRequest", "invalid query parameters")
			return
		}
		formats, present := query["format"]
		if present && (len(formats) != 1 || formats[0] != "raw") {
			writeCredentialError(w, http.StatusBadRequest, "InvalidFormat", "format must be omitted or equal raw")
			return
		}
		roleARN := query.Get("roleArn")
		if roleARN == "" {
			roleARN = defaultRoleARN
		}
		if roleARN == "" {
			writeCredentialError(w, http.StatusBadRequest, "MissingRoleArn", "missing roleArn")
			return
		}
		if !validRoleIdentifier(roleARN) {
			writeCredentialError(w, http.StatusBadRequest, "InvalidRoleArn", "invalid roleArn format")
			return
		}
		if len(allowedARNs) > 0 {
			if _, ok := allowedARNs[roleARN]; !ok {
				writeCredentialError(w, http.StatusForbidden, "RoleNotAllowed", "roleArn not allowed")
				return
			}
		}
		resp, err := s.serveDeveloperAPIResponse(r.Context(), roleARN)
		if err != nil {
			if !errors.Is(err, errCredentialRefreshUnavailable) {
				log.Printf("fetch credential failed: roleArn=%s err=%v", MaskARN(roleARN), err)
			}
			writeCredentialError(w, http.StatusBadGateway, "CredentialUnavailable", "failed to obtain credential")
			return
		}
		resp, err = responseForClient(resp, s.clock.Now())
		if err != nil {
			writeCredentialError(w, http.StatusBadGateway, "InvalidCredential", "failed to obtain valid credential")
			return
		}
		var payload any = resp
		if !present {
			payload, err = convertToCloudContract(resp)
			if err != nil {
				writeCredentialError(w, http.StatusBadGateway, "InvalidCredential", "failed to convert credential")
				return
			}
		}
		// Serialize before committing headers, so no partial success body precedes an error.
		body, err := json.Marshal(payload)
		if err != nil {
			writeCredentialError(w, http.StatusInternalServerError, "InternalError", "failed to encode credential")
			return
		}
		if err := recheck(); err != nil {
			writeAuthenticationError(w, err)
			return
		}
		written = true
		if _, err := w.Write(append(body, '\n')); err != nil {
			log.Print("write credential response failed")
		}
	}
}

func writeAuthenticationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, projectedauth.ErrInvalidRequest):
		writeCredentialError(w, http.StatusBadRequest, "InvalidAuthentication", "invalid authentication header")
	case errors.Is(err, projectedauth.ErrUnauthorized):
		writeCredentialError(w, http.StatusUnauthorized, "Unauthorized", "credential authentication required")
	default:
		writeCredentialError(w, http.StatusServiceUnavailable, "AuthenticationUnavailable", "credential authentication unavailable")
	}
}

// Errors expose stable code/message fields without credentials or upstream bodies.
func writeCredentialError(w http.ResponseWriter, status int, code, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Code: code, Message: message})
}
