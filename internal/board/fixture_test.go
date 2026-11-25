package board_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var sampleScans string

// TestMain builds the canonical sample reels once into a directory that
// outlives every individual test's cleanup.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "emberstripe-board-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(root)
	sampleScans = filepath.Join(root, "scans")
	if err := os.MkdirAll(sampleScans, 0o750); err != nil {
		panic(err)
	}
	cmd := exec.Command("go", "run", "../../cmd/samples", "-out", sampleScans)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func sampleRoot(t *testing.T) string {
	t.Helper()
	return sampleScans
}
