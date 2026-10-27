// Package service registers CarryOn to start at sign-in.
package service

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

var errInstallPaths = errors.New("install needs an executable and a working directory")

func command(ctx context.Context, name string, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
