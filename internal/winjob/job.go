// Package winjob runs child processes inside a Windows Job Object configured
// with kill-on-close, so an agent cannot outlive CarryOn.
package winjob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

// ErrUnsupported is returned when the platform cannot provide job objects.
var ErrUnsupported = errors.New("job objects are only available on Windows")

// Spec describes one supervised process.
type Spec struct {
	Path    string
	Args    []string
	Dir     string
	Env     []string
	Stdout  io.Writer // nil exposes a pipe as Process.Stdout
	Stderr  io.Writer // nil discards stderr
	Timeout time.Duration
}

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

// Result reports how a supervised process ended.
type Result struct {
	ExitCode int
	TimedOut bool
	Duration time.Duration
}

// Start launches the process suspended, assigns it to the job, then resumes it.
func Start(spec Spec) (*Process, error) {
	if spec.Path == "" {
		return nil, errors.New("process path is required")
	}
	command := exec.Command(spec.Path, spec.Args...)
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

// Run starts a process, waits for it, and returns its result.
func Run(ctx context.Context, spec Spec) (Result, error) {
	process, err := Start(spec)
	if err != nil {
		return Result{}, err
	}
	defer process.Close()
	return process.Wait(ctx)
}

var (
	errTimedOut  = errors.New("process timed out")
	errCancelled = errors.New("process cancelled")
)

func classify(waitErr error) (int, bool) {
	switch {
	case errors.Is(waitErr, errTimedOut), errors.Is(waitErr, errCancelled):
		return -1, true
	case waitErr == nil:
		return 0, false
	}
	var exit *exec.ExitError
	if errors.As(waitErr, &exit) {
		return exit.ExitCode(), false
	}
	return -1, false
}
