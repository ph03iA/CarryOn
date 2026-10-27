//go:build linux

package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// unitName is the systemd user unit CarryOn manages.
const unitName = "carryon.service"

// unit builds the user service that runs watch and restarts it on failure.
// The systemd user manager has a minimal PATH, so the installing shell's PATH
// is recorded to keep claude and codex (often in ~/.local/bin) findable.
func unit(exePath, workDir, searchPath string) string {
	environment := ""
	if searchPath != "" {
		environment = "Environment=" + quote("PATH="+searchPath) + "\n"
	}
	return fmt.Sprintf(`[Unit]
Description=CarryOn keeps Claude Code and Codex work going through usage limits.

[Service]
Type=simple
ExecStart=%s watch
WorkingDirectory=%s
%sRestart=on-failure
RestartSec=60

[Install]
WantedBy=default.target
`, quote(exePath), quote(workDir), environment)
}

// quote makes a value safe as one systemd command or directory argument.
func quote(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`)
	return `"` + replacer.Replace(value) + `"`
}

func unitPath() (string, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate config directory: %w", err)
	}
	return filepath.Join(config, "systemd", "user", unitName), nil
}

// Install writes the user unit and enables it now.
func Install(ctx context.Context, exePath, workDir string) (string, error) {
	if exePath == "" || workDir == "" {
		return "", errInstallPaths
	}
	path, err := unitPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create systemd user directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(unit(exePath, workDir, os.Getenv("PATH"))), 0o644); err != nil {
		return "", fmt.Errorf("write systemd unit: %w", err)
	}
	if _, err := command(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return "", err
	}
	if _, err := command(ctx, "systemctl", "--user", "enable", "--now", unitName); err != nil {
		return "", err
	}
	return "Installed the CarryOn user service; systemd starts it now and restarts it on failure.", nil
}

// Uninstall disables and removes the user unit.
func Uninstall(ctx context.Context) error {
	path, err := unitPath()
	if err != nil {
		return err
	}
	_, _ = command(ctx, "systemctl", "--user", "disable", "--now", unitName)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove systemd unit: %w", err)
	}
	_, _ = command(ctx, "systemctl", "--user", "daemon-reload")
	return nil
}

// Status reports whether the user unit is active and enabled.
func Status(ctx context.Context) (string, error) {
	path, err := unitPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return "not installed", nil
	}
	active, _ := command(ctx, "systemctl", "--user", "is-active", unitName)
	enabled, _ := command(ctx, "systemctl", "--user", "is-enabled", unitName)
	return fmt.Sprintf("active: %s\nenabled: %s", strings.TrimSpace(active), strings.TrimSpace(enabled)), nil
}
