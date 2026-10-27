//go:build darwin

package service

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// label is the launchd label and plist name CarryOn manages.
const label = "dev.carryon"

// plist builds the launch agent that runs watch at login and restarts it.
func plist(exePath, workDir string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>watch</string>
	</array>
	<key>WorkingDirectory</key>
	<string>%s</string>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ProcessType</key>
	<string>Background</string>
</dict>
</plist>
`, label, xmlEscape(exePath), xmlEscape(workDir))
}

func xmlEscape(value string) string {
	var escaped strings.Builder
	_ = xml.EscapeText(&escaped, []byte(value))
	return escaped.String()
}

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), nil
}

// Install writes the launch agent and loads it now.
func Install(ctx context.Context, exePath, workDir string) (string, error) {
	if exePath == "" || workDir == "" {
		return "", errInstallPaths
	}
	path, err := plistPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create LaunchAgents directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(plist(exePath, workDir)), 0o644); err != nil {
		return "", fmt.Errorf("write launch agent: %w", err)
	}
	_, _ = command(ctx, "launchctl", "unload", path)
	if _, err := command(ctx, "launchctl", "load", "-w", path); err != nil {
		return "", err
	}
	return "Installed the CarryOn launch agent; it starts at login and restarts if it exits.", nil
}

// Uninstall unloads and removes the launch agent.
func Uninstall(ctx context.Context) error {
	path, err := plistPath()
	if err != nil {
		return err
	}
	_, _ = command(ctx, "launchctl", "unload", path)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove launch agent: %w", err)
	}
	return nil
}

// Status reports whether the launch agent is loaded and its last exit.
func Status(ctx context.Context) (string, error) {
	path, err := plistPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return "not installed", nil
	}
	output, err := command(ctx, "launchctl", "list", label)
	if err != nil {
		return "installed but not loaded", nil
	}
	return strings.TrimSpace(output), nil
}
