package resume

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/ph03iA/CarryOn/internal/config"
)

// gitSnapshot records the dirty state before a run so CarryOn reports only its
// own changes.
type gitSnapshot struct {
	commit    string
	untracked map[string]bool
}

// captureGit saves the working tree in a stash commit and the untracked files.
func captureGit(ctx context.Context, dir string) gitSnapshot {
	snapshot := gitSnapshot{untracked: map[string]bool{}}
	snapshot.commit = strings.TrimSpace(runGit(ctx, dir, "stash", "create"))
	for _, path := range splitLines(runGit(ctx, dir, "ls-files", "--others", "--exclude-standard")) {
		snapshot.untracked[path] = true
	}
	return snapshot
}

// changes reports files, stat and diff since the snapshot.
func (s gitSnapshot) changes(ctx context.Context, dir string) ([]string, string, string) {
	diffArgs := []string{"diff"}
	if s.commit != "" {
		diffArgs = append(diffArgs, s.commit)
	}
	files := splitLines(runGit(ctx, dir, append(append([]string{}, diffArgs...), "--name-only")...))
	stat := runGit(ctx, dir, append(append([]string{}, diffArgs...), "--stat")...)
	excerpt := runGit(ctx, dir, diffArgs...)
	if s.commit == "" {
		// Without a stash commit, include staged changes too.
		files = append(files, splitLines(runGit(ctx, dir, "diff", "--cached", "--name-only"))...)
		stat += runGit(ctx, dir, "diff", "--cached", "--stat")
		excerpt += runGit(ctx, dir, "diff", "--cached")
	}
	for _, path := range splitLines(runGit(ctx, dir, "ls-files", "--others", "--exclude-standard")) {
		if !s.untracked[path] {
			files = append(files, path)
		}
	}
	return uniqueFiles(files, 50), summarize(stat), summarize(excerpt)
}

func runGit(ctx context.Context, dir string, args ...string) string {
	commandCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	command := exec.CommandContext(commandCtx, "git", gitArgs(args)...)
	command.Dir = dir
	command.Env = gitEnv()
	output, err := command.Output()
	if err != nil {
		return ""
	}
	return string(output)
}

// gitArgs disables fsmonitor so every diff reflects the tree immediately.
func gitArgs(args []string) []string {
	return append([]string{"-c", "core.fsmonitor=false"}, args...)
}

// gitEnv keeps the API keys out of git subprocesses.
func gitEnv() []string { return config.ChildEnvironment() }

func splitLines(text string) []string {
	lines := make([]string, 0)
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

func uniqueFiles(files []string, limit int) []string {
	seen := make(map[string]bool)
	unique := make([]string, 0, len(files))
	for _, file := range files {
		file = strings.Trim(strings.TrimSpace(file), `"`)
		if file == "" || seen[file] {
			continue
		}
		seen[file] = true
		unique = append(unique, file)
		if len(unique) >= limit {
			break
		}
	}
	return unique
}
