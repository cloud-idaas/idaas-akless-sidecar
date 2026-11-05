package credentials

import (
	"fmt"
	"log"
	"path"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"
)

// logRefreshPanic records code locations without the panic payload or arguments,
// which may contain credentials. Use base filenames to avoid personal build paths.
func logRefreshPanic() {
	var callers [16]uintptr
	frames := runtime.CallersFrames(callers[:runtime.Callers(3, callers[:])])
	var locations []string
	for {
		frame, more := frames.Next()
		locations = append(locations, fmt.Sprintf("%s(%s:%d)", frame.Function, path.Base(frame.File), frame.Line))
		if !more {
			break
		}
	}
	log.Printf("credential refresh panic stack: %s", strings.Join(locations, " <- "))
}

// validRoleIdentifier bounds the opaque role ID; IDaaS validates format and permissions.
func validRoleIdentifier(value string) bool {
	if value == "" || len(value) > 2048 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// MaskARN hides recognized account IDs; unknown identifiers are fully redacted.
func MaskARN(arn string) string {
	if arn == "" {
		return "(none)"
	}
	switch {
	case strings.HasPrefix(arn, "acs:ram::"), strings.HasPrefix(arn, "arn:aws:iam::"):
		sep := strings.Index(arn, "::")
		tail := arn[sep+2:]
		colon := strings.Index(tail, ":")
		if sep >= 0 && colon > 0 {
			return arn[:sep] + "::********" + tail[colon:]
		}
	case strings.HasPrefix(arn, "qcs::cam::uin/"):
		rest := arn[len("qcs::cam::uin/"):]
		colon := strings.Index(rest, ":")
		if colon > 0 {
			return arn[:len("qcs::cam::uin/")] + "********" + rest[colon:]
		}
	}
	return "(redacted)"
}
