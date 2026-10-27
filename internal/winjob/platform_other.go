//go:build !windows && !linux

package winjob

import "syscall"

func setDeathSignal(*syscall.SysProcAttr) {}
