package resume

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fakeBinary(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	return path
}

func fakeEnv(mode string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "CARRYON_FAKE_MODE=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "CARRYON_FAKE_MODE="+mode)
}

func TestCommandMatchesTheSpec(t *testing.T) {
	claude, err := Command("claude", "sid-1", "do the thing")
	if err != nil {
		t.Fatal(err)
	}
	wantClaude := []string{"--resume", "sid-1", "-p", "do the thing", "--permission-mode", "acceptEdits", "--output-format", "json"}
	if !reflect.DeepEqual(claude, wantClaude) {
		t.Fatalf("claude argv\n got %q\nwant %q", claude, wantClaude)
	}

	codex, err := Command("codex", "sid-2", "do the thing")
	if err != nil {
		t.Fatal(err)
	}
	wantCodex := []string{"exec", "resume", "sid-2", "do the thing", "-c", `sandbox_mode="workspace-write"`}
	if !reflect.DeepEqual(codex, wantCodex) {
		t.Fatalf("codex argv\n got %q\nwant %q", codex, wantCodex)
	}
}

func TestCommandRejectsUnknownProvider(t *testing.T) {
	if _, err := Command("gemini", "sid", "p"); err == nil {
		t.Fatal("want an error for an unknown provider")
	}
}

func TestClaudeRunWritesFile(t *testing.T) {
	dir := t.TempDir()
	runner := Runner{ClaudeBin: fakeBinary(t), Env: fakeEnv("claude"), Timeout: 30 * time.Second}
	outcome, err := runner.Run(context.Background(), Spec{
		Provider: "claude", SessionID: "sid", Cwd: dir, Prompt: "continue",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if outcome.ExitCode != 0 {
		t.Fatalf("unexpected exit code: %d", outcome.ExitCode)
	}
	if _, err := os.Stat(filepath.Join(dir, "resumed.txt")); err != nil {
		t.Fatalf("fake CLI did not write its file: %v", err)
	}
	if !strings.Contains(outcome.LastMessage, "Finished the task") {
		t.Fatalf("unexpected last message: %q", outcome.LastMessage)
	}
}

func TestCodexRunCapturesLastMessageAndTests(t *testing.T) {
	dir := t.TempDir()
	runner := Runner{CodexBin: fakeBinary(t), Env: fakeEnv("codex"), Timeout: 30 * time.Second}
	outcome, err := runner.Run(context.Background(), Spec{
		Provider: "codex", SessionID: "sid", Cwd: dir, Prompt: "continue",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if outcome.LastMessage != "Done: wrote codex-output.txt" {
		t.Fatalf("unexpected last message: %q", outcome.LastMessage)
	}
	if !strings.Contains(outcome.TestOutput, "ok  example") {
		t.Fatalf("test output not captured: %q", outcome.TestOutput)
	}
}

func TestRunRejectsMissingFolder(t *testing.T) {
	runner := Runner{ClaudeBin: fakeBinary(t), Env: fakeEnv("claude")}
	_, err := runner.Run(context.Background(), Spec{
		Provider: "claude", SessionID: "sid", Cwd: filepath.Join(t.TempDir(), "missing"), Prompt: "p",
	})
	if err == nil {
		t.Fatal("want an error for a missing folder")
	}
}

func TestRunReportsNonZeroExit(t *testing.T) {
	dir := t.TempDir()
	runner := Runner{ClaudeBin: fakeBinary(t), Env: fakeEnv("fail"), Timeout: 30 * time.Second}
	outcome, err := runner.Run(context.Background(), Spec{
		Provider: "claude", SessionID: "sid", Cwd: dir, Prompt: "p",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if outcome.ExitCode != 3 {
		t.Fatalf("want exit code 3, got %d", outcome.ExitCode)
	}
}

func TestTimeoutKillsTheAgent(t *testing.T) {
	dir := t.TempDir()
	runner := Runner{ClaudeBin: fakeBinary(t), Env: fakeEnv("slow"), Timeout: 700 * time.Millisecond}
	started := time.Now()
	outcome, err := runner.Run(context.Background(), Spec{
		Provider: "claude", SessionID: "sid", Cwd: dir, Prompt: "p",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !outcome.TimedOut {
		t.Fatal("expected a timeout")
	}
	if time.Since(started) > 10*time.Second {
		t.Fatal("timeout did not return promptly")
	}
	assertStopped(t, filepath.Join(dir, "heartbeat.txt"))
}

// assertStopped proves the heartbeat file stops changing.
func assertStopped(t *testing.T, path string) {
	t.Helper()
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("heartbeat was never written: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	after, err := os.Stat(path)
	if err != nil {
		return
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("agent is still running after the timeout")
	}
}

func TestEnvironmentKeepsSecretsOut(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "re_canary_value")
	t.Setenv("GEMINI_API_KEY", "gm_canary_value")
	t.Setenv("CARRYON_FAKE_MODE", "env")
	dir := t.TempDir()
	// Env is nil so the real scrubbing path runs.
	runner := Runner{ClaudeBin: fakeBinary(t), Timeout: 30 * time.Second}
	if _, err := runner.Run(context.Background(), Spec{
		Provider: "claude", SessionID: "sid", Cwd: dir, Prompt: "p",
	}); err != nil {
		t.Fatalf("run: %v", err)
	}
	dump, err := os.ReadFile(filepath.Join(dir, "env.txt"))
	if err != nil {
		t.Fatalf("env dump: %v", err)
	}
	if strings.Contains(string(dump), "re_canary_value") || strings.Contains(string(dump), "gm_canary_value") {
		t.Fatal("a secret leaked into the child environment")
	}
}

func TestTestExcerptOnlyWhenTestsRan(t *testing.T) {
	if got := testExcerpt("just some output\nFAILED to compile"); got != "" {
		t.Fatalf("must not report tests when none ran: %q", got)
	}
	got := testExcerpt("$ go test ./...\nok  example  0.1s\nFAIL example/two")
	if !strings.Contains(got, "ok  example") || !strings.Contains(got, "FAIL example/two") {
		t.Fatalf("unexpected excerpt: %q", got)
	}
}

func TestRunReportsOnlyChangesMadeDuringTheRun(t *testing.T) {
	dir := t.TempDir()
	runGitForTest(t, dir, "init")
	runGitForTest(t, dir, "config", "user.email", "test@example.com")
	runGitForTest(t, dir, "config", "user.name", "Test")
	os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("base\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "mine.txt"), []byte("base\n"), 0o600)
	runGitForTest(t, dir, "add", "-A")
	runGitForTest(t, dir, "commit", "-m", "baseline")

	// Pre-existing dirty work that CarryOn must not claim.
	os.WriteFile(filepath.Join(dir, "mine.txt"), []byte("my local edit\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "mine-untracked.txt"), []byte("mine\n"), 0o600)

	runner := Runner{ClaudeBin: fakeBinary(t), Env: fakeEnv("claude_edit"), Timeout: 30 * time.Second}
	outcome, err := runner.Run(context.Background(), Spec{
		Provider: "claude", SessionID: "sid", Cwd: dir, Prompt: "continue",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, outcome.Files, "tracked.txt")
	assertContains(t, outcome.Files, "resumed.txt")
	assertMissing(t, outcome.Files, "mine.txt")
	assertMissing(t, outcome.Files, "mine-untracked.txt")
	if strings.Contains(outcome.DiffExcerpt, "my local edit") {
		t.Fatalf("diff excerpt includes pre-existing work:\n%s", outcome.DiffExcerpt)
	}
}

func runGitForTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func assertContains(t *testing.T, files []string, want string) {
	t.Helper()
	for _, file := range files {
		if file == want {
			return
		}
	}
	t.Fatalf("%s missing from %v", want, files)
}

func assertMissing(t *testing.T, files []string, unwanted string) {
	t.Helper()
	for _, file := range files {
		if file == unwanted {
			t.Fatalf("%s must not be reported: %v", unwanted, files)
		}
	}
}

func TestGitArgsDisableFSMonitor(t *testing.T) {
	got := gitArgs([]string{"status", "--porcelain"})
	want := []string{"-c", "core.fsmonitor=false", "status", "--porcelain"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestGitEnvKeepsSecretsOut(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "re_canary_value")
	t.Setenv("GEMINI_API_KEY", "gm_canary_value")
	joined := strings.Join(gitEnv(), "\n")
	if strings.Contains(joined, "re_canary_value") || strings.Contains(joined, "gm_canary_value") {
		t.Fatal("a secret leaked into git's environment")
	}
}

func TestCleanRepoDiffsFromHead(t *testing.T) {
	dir := newGitRepo(t)
	snapshot := captureGit(context.Background(), dir)

	os.WriteFile(filepath.Join(dir, "agent.txt"), []byte("agent\n"), 0o600)
	runGitForTest(t, dir, "add", "-A")
	runGitForTest(t, dir, "commit", "-m", "agent commit")

	files, _, excerpt := snapshot.changes(context.Background(), dir)
	assertContains(t, files, "agent.txt")
	if !strings.Contains(excerpt, "agent.txt") {
		t.Fatalf("diff excerpt misses the agent commit:\n%s", excerpt)
	}
}

func newGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitForTest(t, dir, "init")
	runGitForTest(t, dir, "config", "user.email", "test@example.com")
	runGitForTest(t, dir, "config", "user.name", "Test")
	os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o600)
	runGitForTest(t, dir, "add", "-A")
	runGitForTest(t, dir, "commit", "-m", "baseline")
	return dir
}

func TestClaudeLastMessageIgnoresStderr(t *testing.T) {
	dir := t.TempDir()
	runner := Runner{ClaudeBin: fakeBinary(t), Env: fakeEnv("claude_stderr"), Timeout: 30 * time.Second}
	outcome, err := runner.Run(context.Background(), Spec{
		Provider: "claude", SessionID: "sid", Cwd: dir, Prompt: "continue",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if outcome.LastMessage != "Result from stdout" {
		t.Fatalf("unexpected last message: %q", outcome.LastMessage)
	}
	if strings.Contains(outcome.LastMessage, "warning") {
		t.Fatal("stderr leaked into the last message")
	}
}
