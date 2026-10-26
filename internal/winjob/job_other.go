//go:build !windows

package winjob

import (
	"context"
	"errors"
	"os/exec"
	"time"
)

// Job is a no-op placeholder off Windows.
type Job struct{}

func newJob() (*Job, error) { return &Job{}, nil }

func (j *Job) Assign(int) error { return ErrUnsupported }

func (j *Job) Terminate() {}

func (j *Job) Close() {}

func startSuspended(*exec.Cmd) error { return ErrUnsupported }

func resumeProcess(int) error { return ErrUnsupported }

func killProcess(*exec.Cmd) {}

func Run(context.Context, Spec) (Result, error) {
	return Result{}, ErrUnsupported
}

var _ = time.Second
