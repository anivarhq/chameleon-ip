package desktop

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// superviseChild ties ffmpeg's lifetime to ours.
//
// Measured on Windows: after a force-kill of the engine, ffmpeg usually exits
// on its own, because its next write finds the pipe broken. That covers the
// common case and not the bad one — a camera that has stalled produces no
// writes, so there is nothing to fail on, and ffmpeg sits there holding the
// camera with its light on. A job object marked kill-on-close is honoured
// even when no cleanup code of ours runs: when our handle goes, so does the
// child.
func superviseChild(cmd *exec.Cmd) (func(), error) {
	if cmd.Process == nil {
		return func() {}, nil
	}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return func() {}, err
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return func() {}, err
	}

	handle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return func() {}, err
	}
	defer windows.CloseHandle(handle)

	if err := windows.AssignProcessToJobObject(job, handle); err != nil {
		_ = windows.CloseHandle(job)
		return func() {}, err
	}

	// The job handle must stay open for as long as ffmpeg should live.
	return func() { _ = windows.CloseHandle(job) }, nil
}
