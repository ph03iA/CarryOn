package session

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestScannerRootsFollowProviderHomes(t *testing.T) {
	claude := t.TempDir()
	codex := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("CODEX_HOME", codex)

	scanner := NewScanner()
	if scanner.ClaudeRoot != filepath.Join(claude, "projects") {
		t.Fatalf("unexpected Claude root: %s", scanner.ClaudeRoot)
	}
	if scanner.CodexRoot != filepath.Join(codex, "sessions") {
		t.Fatalf("unexpected Codex root: %s", scanner.CodexRoot)
	}
}

func TestScannerDefaultsUnderHome(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}

	scanner := NewScanner()
	if scanner.ClaudeRoot != filepath.Join(home, ".claude", "projects") {
		t.Fatalf("unexpected Claude root: %s", scanner.ClaudeRoot)
	}
	if scanner.CodexRoot != filepath.Join(home, ".codex", "sessions") {
		t.Fatalf("unexpected Codex root: %s", scanner.CodexRoot)
	}
}
