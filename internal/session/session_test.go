package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func claudeScanner(scenario string) Scanner {
	return Scanner{ClaudeRoot: filepath.Join("testdata", "claude", scenario), MaxBytes: 8 << 20}
}

func codexScanner(scenario string) Scanner {
	return Scanner{CodexRoot: filepath.Join("testdata", "codex", scenario), MaxBytes: 8 << 20}
}

func TestFindClaudeCutOff(t *testing.T) {
	found, ok, err := claudeScanner("cutoff").FindCutOff(Claude)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !ok {
		t.Fatal("expected a cut-off session")
	}
	if found.ID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("unexpected id: %s", found.ID)
	}
	if found.Cwd != `C:\work\demo` {
		t.Fatalf("unexpected cwd: %s", found.Cwd)
	}
	if found.CutOffAt.IsZero() {
		t.Fatal("expected a cut-off timestamp")
	}
	if !strings.Contains(found.LastMessage, "read the failing test") {
		t.Fatalf("expected the last assistant message, got %q", found.LastMessage)
	}
}

func TestIgnoresOldErrorFollowedBySuccess(t *testing.T) {
	_, ok, err := claudeScanner("old_error").FindCutOff(Claude)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if ok {
		t.Fatal("a session that recovered must not be reported as cut off")
	}
}

func TestIgnoresTrailingMetadata(t *testing.T) {
	found, ok, err := claudeScanner("trailing_metadata").FindCutOff(Claude)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !ok {
		t.Fatal("metadata after the error must not hide the cut-off")
	}
	continued, err := claudeScanner("trailing_metadata").Continued(found)
	if err != nil {
		t.Fatalf("continued check: %v", err)
	}
	if continued {
		t.Fatal("metadata is not a real event, so the session is not continued")
	}
}

func TestDetectsAlreadyContinued(t *testing.T) {
	dir := t.TempDir()
	source, err := os.ReadFile(filepath.Join("testdata", "claude", "cutoff", "proj-a", "11111111-1111-4111-8111-111111111111.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "proj-a")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(target, "11111111-1111-4111-8111-111111111111.jsonl")
	if err := os.WriteFile(path, source, 0o600); err != nil {
		t.Fatal(err)
	}

	scanner := Scanner{ClaudeRoot: dir, MaxBytes: 8 << 20}
	found, ok, err := scanner.FindCutOff(Claude)
	if err != nil || !ok {
		t.Fatalf("scan: ok=%v err=%v", ok, err)
	}

	appended := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Continuing after the reset."}]},"uuid":"d0000000-0000-4000-8000-000000000001","timestamp":"2026-10-26T18:00:00.000Z","cwd":"C:\\work\\demo"}`
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(appended + "\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()

	continued, err := scanner.Continued(found)
	if err != nil {
		t.Fatalf("continued check: %v", err)
	}
	if !continued {
		t.Fatal("expected the session to be detected as already continued")
	}
}

func TestFindCodexCutOff(t *testing.T) {
	found, ok, err := codexScanner("cutoff").FindCutOff(Codex)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !ok {
		t.Fatal("expected a codex cut-off session")
	}
	if found.ID != "01a17756-0de8-7fd1-87b3-57ef7c572450" {
		t.Fatalf("unexpected id: %s", found.ID)
	}
	if found.Cwd != `C:\work\codexdemo` {
		t.Fatalf("unexpected cwd: %s", found.Cwd)
	}
}

func TestIgnoresCodexOldErrorFollowedBySuccess(t *testing.T) {
	_, ok, err := codexScanner("old_error").FindCutOff(Codex)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if ok {
		t.Fatal("a recovered codex session must not be reported as cut off")
	}
}

func TestFindByID(t *testing.T) {
	found, err := claudeScanner("cutoff").FindByID(Claude, "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatalf("find by id: %v", err)
	}
	if found.Provider != Claude {
		t.Fatalf("unexpected provider: %s", found.Provider)
	}
	if _, err := claudeScanner("cutoff").FindByID(Claude, "missing"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestLastMessagePrefersTaskComplete(t *testing.T) {
	scanner := codexScanner("old_error")
	found, err := scanner.FindByID(Codex, "01a17756-0de8-7fd1-87b3-57ef7c572451")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got := scanner.LastMessage(found); !strings.Contains(got, "parser handles empty input") {
		t.Fatalf("unexpected last message: %q", got)
	}
}
