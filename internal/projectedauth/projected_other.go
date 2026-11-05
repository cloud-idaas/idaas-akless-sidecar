//go:build !linux

package projectedauth

import "os"

// Projected production mounts are supported in Linux containers only.
func requireReadonlyFilesystem(*os.File) error { return ErrUnavailable }
