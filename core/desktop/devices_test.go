package desktop

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A missing ffmpeg must be reported as missing. It used to surface as an empty
// camera list, and the window told the user this machine had no camera.
func TestFindFFmpeg(t *testing.T) {
	dir := t.TempDir()

	if _, err := FindFFmpeg(filepath.Join(dir, "absent")); err == nil ||
		!strings.Contains(err.Error(), "ffmpeg not found") {
		t.Fatalf("missing ffmpeg: got %v, want an 'ffmpeg not found' error", err)
	}

	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name = "ffmpeg.exe"
	}
	fake := filepath.Join(dir, name)
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := FindFFmpeg(fake)
	if err != nil || got != fake {
		t.Fatalf("explicit path: got %q, %v; want %q", got, err, fake)
	}
}
