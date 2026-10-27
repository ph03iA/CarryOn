//go:build !windows

package winjob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Process is a running child in its own process group.
type Process struct {
	cmd     *exec.Cmd
	pgid    int
	timeout time.Duration

	mu   sync.Mutex
	done bool

	// Stdin is always a pipe, so a probe can speak JSON-RPC.
	Stdin io.WriteCloser
	// Stdout is set only when Spec.Stdout was nil.
	Stdout io.ReadCloser
}

// Start launches the process in its own process group.
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

	attr := &syscall.SysProcAttr{Setpgid: true}
	setDeathSignal(attr)
	command.SysProcAttr = attr
	if err := command.Start(); err != nil {
		stdin.Close()
		if stdout != nil {
			stdout.Close()
		}
		return nil, fmt.Errorf("start process: %w", err)
	}
	if spec.CloseStdin {
		stdin.Close()
	}
	return &Process{
		cmd: command, pgid: command.Process.Pid, timeout: spec.Timeout,
		Stdin: stdin, Stdout: stdout,
	}, nil
}

// Terminate kills the whole process group.
func (p *Process) Terminate() {
	if p == nil {
		return
	}
	p.mu.Lock()
	done, pgid := p.done, p.pgid
	p.mu.Unlock()
	if done {
		return
	}
	killGroup(pgid)
}

// Wait waits for exit, enforcing the timeout by killing the process group.
func (p *Process) Wait(ctx context.Context) (Result, error) {
	if p == nil || p.cmd == nil {
		return Result{}, errors.New("process is not running")
	}
	started := time.Now()
	waitErr := p.waitWithTimeout(ctx)
	p.mu.Lock()
	p.done = true
	p.mu.Unlock()
	result := Result{Duration: time.Since(started)}
	result.ExitCode, result.TimedOut = classify(waitErr)
	return result, nil
}

// Close closes stdin and kills the group if the process is still running.
func (p *Process) Close() {
	if p == nil {
		return
	}
	if p.Stdin != nil {
		p.Stdin.Close()
	}
	p.mu.Lock()
	done, pgid := p.done, p.pgid
	p.mu.Unlock()
	if !done {
		killGroup(pgid)
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
		p.Terminate()
		<-done
		return errTimedOut
	case <-ctx.Done():
		p.Terminate()
		<-done
		return errCancelled
	}
}

func killGroup(pgid int) {
	if pgid <= 0 {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}
