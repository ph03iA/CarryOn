// Package winjob runs child processes so they cannot outlive CarryOn.
package winjob

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

// waitDelay bounds how long Wait keeps reading pipes after the process exits.
var waitDelay = 10 * time.Second

// Spec describes one supervised process.
type Spec struct {
	Path    string
	Args    []string
	Dir     string
	Env     []string
	Stdout  io.Writer // nil exposes a pipe as Process.Stdout
	Stderr  io.Writer // nil discards stderr
	Timeout time.Duration
	// CloseStdin closes the stdin pipe after start, so the child sees EOF.
	CloseStdin bool
}

// Result reports how a supervised process ended.
type Result struct {
	ExitCode int
	TimedOut bool
	Duration time.Duration
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
	case errors.Is(waitErr, exec.ErrWaitDelay):
		// The process exited; only a background child held the pipes open.
		return 0, false
	case waitErr == nil:
		return 0, false
	}
	var exit *exec.ExitError
	if errors.As(waitErr, &exit) {
		return exit.ExitCode(), false
	}
	return -1, false
}
