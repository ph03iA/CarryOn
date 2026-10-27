//go:build !windows && !darwin && !linux

package service

import (
	"context"
	"errors"
)

// Install is unsupported on this platform.
func Install(context.Context, string, string) (string, error) {
	return "", errors.New("service install is not supported on this platform")
}

// Uninstall is unsupported on this platform.
func Uninstall(context.Context) error {
	return errors.New("service uninstall is not supported on this platform")
}

// Status is unsupported on this platform.
func Status(context.Context) (string, error) {
	return "", errors.New("service status is not supported on this platform")
}
