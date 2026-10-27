//go:build windows

package winjob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Process is a running child owned by a kill-on-close job.
type Process struct {
	cmd     *exec.Cmd
	job     *Job
	timeout time.Duration

	// Stdin is always a pipe, so a probe can speak JSON-RPC.
	Stdin io.WriteCloser
	// Stdout is set only when Spec.Stdout was nil.
	Stdout io.ReadCloser
}

// Start launches the process suspended, assigns it to the job, then resumes it.
func Start(spec Spec) (*Process, error) {
	if spec.Path == "" {
		return nil, errors.New("process path is required")
	}
	command := exec.Command(spec.Path, spec.Args...)
	command.WaitDelay = waitDelay
	command.Dir = spec.Dir
	command.Env = spec.Env
	command.Stderr = spec.Stderr
	if command.Stderr == nil {
		command.Stderr = io.Discard
	}

	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open stdin: %w", err)
	}
	var stdout io.ReadCloser
	if spec.Stdout == nil {
		stdout, err = command.StdoutPipe()
		if err != nil {
			stdin.Close()
			return nil, fmt.Errorf("open stdout: %w", err)
		}
	} else {
		command.Stdout = spec.Stdout
	}

	job, err := newJob()
	if err != nil {
		stdin.Close()
		if stdout != nil {
			stdout.Close()
		}
		return nil, err
	}
	if err := startSuspended(command); err != nil {
		job.Close()
		stdin.Close()
		if stdout != nil {
			stdout.Close()
		}
		return nil, err
	}
	if err := job.Assign(command.Process.Pid); err != nil {
		job.Terminate()
		killProcess(command)
		job.Close()
		return nil, fmt.Errorf("assign process to job object: %w", err)
	}
	if err := resumeProcess(command.Process.Pid); err != nil {
		job.Terminate()
		killProcess(command)
		job.Close()
		return nil, fmt.Errorf("resume suspended process: %w", err)
	}
	if spec.CloseStdin {
		stdin.Close()
	}
	return &Process{cmd: command, job: job, timeout: spec.Timeout, Stdin: stdin, Stdout: stdout}, nil
}

// Terminate kills every process in the job.
func (p *Process) Terminate() {
	if p == nil || p.job == nil {
		return
	}
	p.job.Terminate()
}

// Wait waits for exit, enforcing the timeout by terminating the whole job.
func (p *Process) Wait(ctx context.Context) (Result, error) {
	if p == nil || p.cmd == nil {
		return Result{}, errors.New("process is not running")
	}
	started := time.Now()
	waitErr := p.waitWithTimeout(ctx)
	p.job.Close()
	result := Result{Duration: time.Since(started)}
	result.ExitCode, result.TimedOut = classify(waitErr)
	return result, nil
}

// Close releases the job, which also kills anything still running.
func (p *Process) Close() {
	if p == nil {
		return
	}
	if p.Stdin != nil {
		p.Stdin.Close()
	}
	if p.job != nil {
		p.job.Close()
	}
}

func (p *Process) waitWithTimeout(ctx context.Context) error {
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()

	var expired <-chan time.Time
	if p.timeout > 0 {
		timer := time.NewTimer(p.timeout)
		defer timer.Stop()
		expired = timer.C
	}

	select {
	case err := <-done:
		return err
	case <-expired:
		p.job.Terminate()
		<-done
		return errTimedOut
	case <-ctx.Done():
		p.job.Terminate()
		<-done
		return errCancelled
	}
}

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
