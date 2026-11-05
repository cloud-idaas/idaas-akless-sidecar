package projectedauth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTrustedProjectionDirectory(t *testing.T) {
	for _, tc := range []struct {
		mode  os.FileMode
		owner uint32
		want  bool
	}{
		{os.ModeDir | 0755, 0, true}, {os.ModeDir | 0550, 0, true},
		{os.ModeDir | 0775, 0, true}, {os.ModeDir | os.ModeSticky | os.ModeSetgid | 0777, 0, true},
		{os.ModeDir | 0755, 10001, false}, {0440, 0, false},
	} {
		if got := trustedProjectionDirectory(tc.mode, tc.owner); got != tc.want {
			t.Fatalf("mode=%v owner=%d accepted=%v", tc.mode, tc.owner, got)
		}
	}
}

func TestProjectedConstructorRejectsOrdinaryDirectory(t *testing.T) {
	_, path, _, _ := fixture(t)
	if a, err := NewProjected(path, testPod); !errors.Is(err, ErrUnavailable) {
		if a != nil {
			_ = a.Close()
		}
		t.Fatal("ordinary writable filesystem accepted", err)
	}
	if a, err := NewProjected("relative/token", testPod); !errors.Is(err, ErrUnavailable) {
		if a != nil {
			_ = a.Close()
		}
		t.Fatal("invalid path accepted", err)
	}
}

func TestClosedProjectionHandlesFailClosed(t *testing.T) {
	a, _, _, _ := fixture(t)
	if err := a.root.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.validateProjection(); err == nil {
		t.Fatal("closed mount root accepted")
	}
	dir, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := dir.Close(); err != nil {
		t.Fatal(err)
	}
	if err := requireReadonlyFilesystem(dir); err == nil {
		t.Fatal("closed descriptor accepted")
	}
}

func TestProductionReadRequiresRootOwnedToken(t *testing.T) {
	a, path, token, _ := fixture(t)
	if os.Geteuid() == 0 {
		if err := os.Chown(path, 10001, -1); err != nil {
			t.Fatal(err)
		}
	}
	a.requireRootOwner = true
	if _, err := authenticate(a, token); !errors.Is(err, ErrUnavailable) {
		t.Fatal("non-root token file accepted in production mode", err)
	}
}

// Run the Linux test binary as non-root with /fixtures/ro mounted read-only and
// the same fixture volume at /fixtures/rw writable. No real credentials needed.
func TestProjectedMountRuntime(t *testing.T) {
	base := os.Getenv("AKLESS_PROJECTED_TEST_ROOT")
	if base == "" {
		t.Skip("requires isolated Linux mount fixtures")
	}
	for _, tc := range []struct {
		path      string
		wantError bool
	}{
		{"ro/valid/token", false}, {"ro/readonly/token", false},
		{"rw/valid/token", true}, {"ro/untrusted-owner/token", true},
		{"ro/kubelet-directory/token", false}, {"ro/untrusted-directory/token", true},
	} {
		t.Run(tc.path, func(t *testing.T) {
			a, err := NewProjected(filepath.Join(base, tc.path), testPod)
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v", err)
			}
			if a != nil {
				defer func() { _ = a.Close() }()
				data, err := os.ReadFile(filepath.Join(base, tc.path))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := authenticate(a, string(data)); err != nil {
					t.Fatal("trusted projected token rejected", err)
				}
			}
		})
	}
}
