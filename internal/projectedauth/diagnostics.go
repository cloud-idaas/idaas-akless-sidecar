package projectedauth

import (
	"errors"
	"log"
	"os"
	"time"
)

// referenceFailure contains only fixed diagnostic codes, never file contents,
// JWT claims, paths or parser/OS error text. Public HTTP errors stay generic.
type referenceFailure string

const (
	reasonInvalidConfiguration referenceFailure = "invalid_configuration"
	reasonInvalidMount         referenceFailure = "invalid_mount"
	reasonNonRootRequired      referenceFailure = "non_root_required"
	reasonFileMissing          referenceFailure = "file_missing"
	reasonFilePermissionDenied referenceFailure = "file_permission_denied"
	reasonFileIOFailed         referenceFailure = "file_io_failed"
	reasonInvalidFile          referenceFailure = "invalid_file"
	reasonUnsafePermissions    referenceFailure = "unsafe_permissions"
	reasonInvalidToken         referenceFailure = "invalid_token"
	reasonInvalidBinding       referenceFailure = "invalid_binding"
	reasonInvalidValidity      referenceFailure = "invalid_validity"
	reasonExpired              referenceFailure = "expired"
	reasonMaximumAgeExceeded   referenceFailure = "maximum_age_exceeded"

	authenticationLogInterval = time.Minute
)

func (r referenceFailure) Error() string { return ErrUnavailable.Error() + ": reason=" + string(r) }
func (r referenceFailure) Unwrap() error { return ErrUnavailable }

func referenceIOFailure(err error) referenceFailure {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return reasonFileMissing
	case errors.Is(err, os.ErrPermission):
		return reasonFilePermissionDenied
	default:
		return reasonFileIOFailed
	}
}

// logReferenceStatus runs under mu. One unchanged outage logs once; changed
// reasons and flapping failures log at most once per minute. A logged outage
// gets one recovery message. Ordinary request rejection and rotation stay quiet.
func (a *Authenticator) logReferenceStatus(now time.Time, err error) {
	if err == nil {
		if a.lastFailureReason != "" {
			log.Print("local credential authentication recovered")
			a.lastFailureReason = ""
		}
		return
	}
	reason := reasonFileIOFailed
	var failure referenceFailure
	if errors.As(err, &failure) {
		reason = failure
	}
	if reason != a.lastFailureReason && (a.lastFailureLogAt.IsZero() || !now.Before(a.lastFailureLogAt.Add(authenticationLogInterval))) {
		log.Printf("WARN local credential authentication unavailable: reason=%s", string(reason))
		a.lastFailureReason = reason
		a.lastFailureLogAt = now
	}
}
