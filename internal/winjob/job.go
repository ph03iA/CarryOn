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
	Stdout  io.Writer
	Stderr  io.Writer
	Timeout time.Duration
}

// Result reports how the supervised process ended.
type Result struct {
	ExitCode int
	TimedOut bool
	Duration time.Duration
}

// Run starts the process suspended, assigns it to a kill-on-close job, then
// resumes it. If CarryOn dies, Windows closes the job handle and terminates the
// whole process tree.
func Run(ctx context.Context, spec Spec) (Result, error) {
	if spec.Path == "" {
		return Result{}, errors.New("process path is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	command := exec.Command(spec.Path, spec.Args...)
	command.Dir = spec.Dir
	command.Env = spec.Env
	command.Stdout = spec.Stdout
	command.Stderr = spec.Stderr

	job, err := newJob()
	if err != nil {
		return Result{}, err
	}
	defer job.Close()

	started := time.Now()
	if err := startSuspended(command); err != nil {
		return Result{}, err
	}
	if err := job.Assign(command.Process.Pid); err != nil {
		killProcess(command)
		return Result{}, fmt.Errorf("assign process to job object: %w", err)
	}
	if err := resumeProcess(command.Process.Pid); err != nil {
		job.Terminate()
		killProcess(command)
		return Result{}, fmt.Errorf("resume suspended process: %w", err)
	}

	waitErr := waitWithTimeout(ctx, command, job, spec.Timeout)
	result := Result{Duration: time.Since(started)}
	result.ExitCode, result.TimedOut = classify(waitErr)
	return result, nil
}

func waitWithTimeout(ctx context.Context, command *exec.Cmd, job *Job, timeout time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()

	var expired <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		expired = timer.C
	}

	select {
	case err := <-done:
		return err
	case <-expired:
		job.Terminate()
		<-done
		return errTimedOut
	case <-ctx.Done():
		job.Terminate()
		<-done
		return errCancelled
	}
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
