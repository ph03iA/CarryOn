// Package resume continues a cut-off session and collects its outcome.
package resume

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/ph03iA/CarryOn/internal/config"
	"github.com/ph03iA/CarryOn/internal/quota"
	"github.com/ph03iA/CarryOn/internal/winjob"
)

// Spec is one session to continue.
type Spec struct {
	Provider  string
	SessionID string
	Cwd       string
	Prompt    string
}

// Outcome is everything CarryOn reports after a run.
type Outcome struct {
	ExitCode    int
	TimedOut    bool
	Duration    time.Duration
	LastMessage string
	Files       []string
	DiffStat    string
	DiffExcerpt string
	TestOutput  string
	RunLog      string
}

// Runner executes the Claude or Codex CLI inside a job object.
type Runner struct {
	ClaudeBin string
	CodexBin  string
	Timeout   time.Duration
	Env       []string
	LogLimit  int
}

// Command returns the exact argv used to continue a session.
func Command(provider, sessionID, prompt string) ([]string, error) {
	switch provider {
	case "claude":
		return []string{"--resume", sessionID, "-p", prompt, "--permission-mode", "acceptEdits"}, nil
	case "codex":
		return []string{"exec", "resume", sessionID, prompt, "-c", `sandbox_mode="workspace-write"`}, nil
	default:
		return nil, fmt.Errorf("unknown provider %q", provider)
	}
}

// Run continues the session and captures its artifacts.
func (r Runner) Run(ctx context.Context, spec Spec) (Outcome, error) {
	if spec.SessionID == "" {
		return Outcome{}, errors.New("resume requires a session id")
	}
	if info, err := os.Stat(spec.Cwd); err != nil || !info.IsDir() {
		return Outcome{}, fmt.Errorf("session folder is missing: %s", spec.Cwd)
	}
	args, err := Command(spec.Provider, spec.SessionID, spec.Prompt)
	if err != nil {
		return Outcome{}, err
	}
	binary, err := r.binary(spec.Provider)
	if err != nil {
		return Outcome{}, err
	}
	lastMessageFile, cleanup, err := r.lastMessageFile(spec.Provider)
	if err != nil {
		return Outcome{}, err
	}
	defer cleanup()
	if lastMessageFile != "" {
		args = append(args, "-o", lastMessageFile)
	}

	snapshot := captureGit(ctx, spec.Cwd)
	log := &tailBuffer{max: r.logLimit()}
	started := time.Now()
	result, err := winjob.Run(ctx, winjob.Spec{
		Path:       binary,
		Args:       args,
		Dir:        spec.Cwd,
		Env:        r.environment(),
		Stdout:     log,
		Stderr:     log,
		Timeout:    r.timeout(),
		CloseStdin: true,
	})
	if err != nil {
		return Outcome{}, err
	}
	outcome := Outcome{
		ExitCode: result.ExitCode,
		TimedOut: result.TimedOut,
		Duration: time.Since(started),
		RunLog:   log.String(),
	}
	outcome.LastMessage = readLastMessage(lastMessageFile, outcome.RunLog)
	outcome.Files, outcome.DiffStat, outcome.DiffExcerpt = snapshot.changes(ctx, spec.Cwd)
	outcome.TestOutput = testExcerpt(outcome.RunLog)
	return outcome, nil
}

func (r Runner) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return 6 * time.Hour
}

func (r Runner) logLimit() int {
	if r.LogLimit > 0 {
		return r.LogLimit
	}
	return 64 * 1024
}

func (r Runner) environment() []string {
	if r.Env != nil {
		return r.Env
	}
	return config.ChildEnvironment()
}

func (r Runner) binary(provider string) (string, error) {
	if provider == "codex" {
		return quota.ResolveBinary("codex", r.CodexBin, quota.CodexFallbacks())
	}
	return quota.ResolveBinary("claude", r.ClaudeBin, nil)
}

// lastMessageFile asks Codex to write its final message to a temp file.
func (r Runner) lastMessageFile(provider string) (string, func(), error) {
	if provider != "codex" {
		return "", func() {}, nil
	}
	file, err := os.CreateTemp("", "carryon-last-*.txt")
	if err != nil {
		return "", func() {}, fmt.Errorf("create last-message file: %w", err)
	}
	path := file.Name()
	file.Close()
	return path, func() { os.Remove(path) }, nil
}

func readLastMessage(path, runLog string) string {
	if path != "" {
		if data, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(data)) != "" {
			return summarize(string(data))
		}
	}
	return summarize(runLog)
}

var testCommandPattern = regexp.MustCompile(`(?i)(go test|npm (run )?test|pnpm test|yarn test|pytest|python -m pytest|cargo test|dotnet test|mvn test|gradle test|jest|vitest)`)

var testOutputPattern = regexp.MustCompile(`(?im)^.*(PASS|FAIL|ok\s+\S+|--- (PASS|FAIL)|Tests:\s+\d|test result:|Ran \d+ tests).*$`)

// testExcerpt returns matching test lines only when the run looks like tests.
func testExcerpt(runLog string) string {
	if !testCommandPattern.MatchString(runLog) {
		return ""
	}
	matches := testOutputPattern.FindAllString(runLog, 40)
	return summarize(strings.Join(matches, "\n"))
}

func summarize(text string) string {
	trimmed := strings.TrimSpace(text)
	const max = 4000
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "\n…"
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	max  int
	data []byte
}

func (b *tailBuffer) Write(chunk []byte) (int, error) {
	b.data = append(b.data, chunk...)
	if len(b.data) > b.max {
		b.data = b.data[len(b.data)-b.max:]
	}
	return len(chunk), nil
}

func (b *tailBuffer) String() string { return string(b.data) }
