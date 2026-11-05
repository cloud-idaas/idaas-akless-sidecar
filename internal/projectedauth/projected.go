package projectedauth

import (
	"os"
	"syscall"
)

// NewProjected is the container entry point. It validates the pinned directory,
// not a second pathname lookup, and enforces root ownership on every token read.
// New remains the lower-level reference-file authenticator for trusted callers.
func NewProjected(path string, pod PodContext) (*Authenticator, error) {
	if os.Geteuid() == 0 {
		return nil, reasonNonRootRequired
	}
	a, err := New(path, pod)
	if err != nil {
		return nil, err
	}
	if err := a.validateProjection(); err != nil {
		_ = a.Close()
		return nil, err
	}
	return a, nil
}

func (a *Authenticator) validateProjection() error {
	dir, err := a.root.Open(".")
	if err != nil {
		return referenceIOFailure(err)
	}
	defer func() { _ = dir.Close() }()
	info, err := dir.Stat()
	if err != nil {
		return referenceIOFailure(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !trustedProjectionDirectory(info.Mode(), stat.Uid) {
		return reasonInvalidMount
	}
	if err := requireReadonlyFilesystem(dir); err != nil {
		return reasonInvalidMount
	}
	a.requireRootOwner = true
	return a.reload(a.now())
}

func trustedProjectionDirectory(mode os.FileMode, owner uint32) bool {
	// Kubelet's projected mount root may be 3777. The mandatory filesystem
	// read-only check in validateProjection, rather than mode bits, prevents writes.
	return mode.IsDir() && owner == 0
}
