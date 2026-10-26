//go:build windows

package winjob

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Job owns a Windows job object handle.
type Job struct {
	handle windows.Handle
}

func newJob() (*Job, error) {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create job object: %w", err)
	}
	job := &Job{handle: handle}
	if err := job.enableKillOnClose(); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	return job, nil
}

func (j *Job) enableKillOnClose() error {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err := windows.SetInformationJobObject(
		j.handle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		return fmt.Errorf("enable kill-on-close: %w", err)
	}
	return nil
}

// Assign attaches a running process to the job.
func (j *Job) Assign(pid int) error {
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	return windows.AssignProcessToJobObject(j.handle, process)
}

// Terminate kills every process in the job.
func (j *Job) Terminate() {
	_ = windows.TerminateJobObject(j.handle, 1)
}

// Close releases the handle. With kill-on-close set, this also terminates any
// remaining process in the job.
func (j *Job) Close() {
	if j == nil || j.handle == 0 {
		return
	}
	_ = windows.CloseHandle(j.handle)
	j.handle = 0
}

func startSuspended(command *exec.Cmd) error {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if err := command.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}
	return nil
}

var procNtResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

func resumeProcess(pid int) error {
	process, err := windows.OpenProcess(windows.PROCESS_SUSPEND_RESUME, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	status, _, _ := procNtResumeProcess.Call(uintptr(process))
	if status != 0 {
		return fmt.Errorf("NtResumeProcess returned 0x%x", status)
	}
	return nil
}

func killProcess(command *exec.Cmd) {
	if command != nil && command.Process != nil {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	}
}
