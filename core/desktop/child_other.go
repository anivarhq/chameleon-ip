//go:build !windows

package desktop

import "os/exec"

// On macOS and Linux the child dies with the pipe: when we exit, ffmpeg's
// stdout has no reader and it stops on its own, and the context we start it
// with signals it first. Nothing more is needed.
func superviseChild(cmd *exec.Cmd) (func(), error) { return func() {}, nil }
