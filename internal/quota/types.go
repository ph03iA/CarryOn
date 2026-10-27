// Package quota reads plan usage without spending any.
package quota

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Window is one usage bucket.
type Window struct {
	Provider    string
	AccountID   string
	ID          string
	Label       string
	UsedPercent float64
	ResetsAt    time.Time
	Duration    time.Duration
}

// Snapshot is one complete reading for a provider.
type Snapshot struct {
	Provider             string
	AccountID            string
	ObservedAt           time.Time
	Windows              []Window
	OrdinaryUsageAllowed *bool
}

// Provider reads one tool's plan usage.
type Provider interface {
	Name() string
	Read(ctx context.Context) (Snapshot, error)
}

// Exhausted reports whether any window in the snapshot is used up.
func (s Snapshot) Exhausted() bool {
	if s.OrdinaryUsageAllowed != nil && !*s.OrdinaryUsageAllowed {
		return true
	}
	for _, window := range s.Windows {
		if window.UsedPercent >= 100 {
			return true
		}
	}
	return false
}

// Label names a window by its length, such as "5-hour" or "weekly".
func Label(duration time.Duration) string {
	const day = 24 * time.Hour
	switch {
	case duration <= 0:
		return "usage"
	case duration == 7*day:
		return "weekly"
	case duration%day == 0:
		return fmt.Sprintf("%d-day", duration/day)
	case duration%time.Hour == 0:
		return fmt.Sprintf("%d-hour", duration/time.Hour)
	default:
		return fmt.Sprintf("%d-minute", duration/time.Minute)
	}
}

// ResolveBinary finds a native executable, rejecting shell shims.
func ResolveBinary(name, configured string, fallbacks []string) (string, error) {
	if strings.TrimSpace(configured) != "" {
		return nativeOnly(configured)
	}
	if found, err := exec.LookPath(name); err == nil && !isShim(found) {
		return found, nil
	}
	for _, candidate := range fallbacks {
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return nativeOnly(candidate)
		}
	}
	return "", fmt.Errorf("%s not found; install it or set its path in config.json", name)
}

func nativeOnly(path string) (string, error) {
	if isShim(path) {
		return "", fmt.Errorf("%s is a shell shim; point CarryOn at the native executable", path)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return absolute, nil
}

// CodexFallbacks lists npm-installed native Codex binaries.
func CodexFallbacks() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return nil
	}
	base := filepath.Join(appData, "npm", "node_modules", "@openai", "codex", "node_modules")
	triples := []string{"x86_64-pc-windows-msvc", "aarch64-pc-windows-msvc"}
	paths := make([]string, 0, len(triples))
	for _, triple := range triples {
		paths = append(paths, filepath.Join(base, "@openai", "codex-win32-x64", "vendor", triple, "bin", "codex.exe"))
	}
	return paths
}
