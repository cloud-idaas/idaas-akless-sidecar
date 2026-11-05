package projectedauth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWriteProjectedMountFixtures is invoked only by verification/projected-auth/run.py
// in a disposable root container. The runtime test uses the same volume as non-root.
func TestWriteProjectedMountFixtures(t *testing.T) {
	base := os.Getenv("AKLESS_PROJECTED_FIXTURE_OUTPUT")
	if base == "" {
		t.Skip("fixture generation is only used by the isolated Linux runner")
	}
	if os.Geteuid() != 0 || base != "/fixtures" {
		t.Fatal("fixture generation requires the isolated root container at /fixtures")
	}
	token := encodeClaims(t, claimsAt(time.Now(), "mount-fixture"))
	for _, tc := range []struct {
		name           string
		directoryOwner int
		tokenOwner     int
		directoryMode  os.FileMode
		tokenMode      os.FileMode
	}{
		{"valid", 0, 0, 0755, 0640},
		{"readonly", 0, 0, 0755, 0440},
		{"untrusted-owner", 0, 10001, 0755, 0440},
		{"kubelet-directory", 0, 0, os.ModeSticky | os.ModeSetgid | 0777, 0440},
		{"untrusted-directory", 10001, 0, 0755, 0440},
	} {
		directory := filepath.Join(base, tc.name)
		if err := os.Mkdir(directory, 0755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, "token")
		if err := os.WriteFile(path, []byte(token), tc.tokenMode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(path, tc.tokenOwner, 20001); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, tc.tokenMode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(directory, tc.directoryOwner, 20001); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(directory, tc.directoryMode); err != nil {
			t.Fatal(err)
		}
	}
}
