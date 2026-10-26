//go:build !windows

package winjob

import (
	"errors"
	"os/exec"
)

// Job is a no-op placeholder off Windows.
type Job struct{}

func newJob() (*Job, error) { return nil, ErrUnsupported }

func (j *Job) Assign(int) error { return ErrUnsupported }

func (j *Job) Terminate() {}

func (j *Job) Close() {}

func startSuspended(*exec.Cmd) error { return ErrUnsupported }

func resumeProcess(int) error { return ErrUnsupported }

func killProcess(*exec.Cmd) {}

var _ = errors.New
