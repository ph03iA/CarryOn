// Package session finds the Claude Code and Codex session a usage limit cut off.
package session

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Provider names used across CarryOn.
const (
	Claude = "claude"
	Codex  = "codex"
)

// Session describes one resumable conversation.
type Session struct {
	Provider    string
	ID          string
	Path        string
	Cwd         string
	CutOffAt    time.Time
	ResumeFrom  int64 // byte offset just past the cut-off error line
	LastMessage string
	ModTime     time.Time
}

var limitPattern = regexp.MustCompile(`(?i)(usage limit|rate.?limit|limit reached|quota exceeded|out of usage|session limit|weekly limit|too many requests|\b429\b)`)

// matchesLimit reports whether text describes an exhausted usage limit.
func matchesLimit(text string) bool { return limitPattern.MatchString(text) }

// ErrNotFound means no matching session log exists.
var ErrNotFound = errors.New("session not found")

// Scanner locates session logs under the provider roots.
type Scanner struct {
	ClaudeRoot string
	CodexRoot  string
	MaxBytes   int64
}

// NewScanner uses the standard roots unless the environment overrides them.
func NewScanner() Scanner {
	return Scanner{
		ClaudeRoot: claudeRoot(),
		CodexRoot:  codexRoot(),
		MaxBytes:   8 << 20,
	}
}

func claudeRoot() string {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, "projects")
	}
	return filepath.Join(homeDir(), ".claude", "projects")
}

func codexRoot() string {
	if dir := strings.TrimSpace(os.Getenv("CODEX_HOME")); dir != "" {
		return filepath.Join(dir, "sessions")
	}
	return filepath.Join(homeDir(), ".codex", "sessions")
}

func homeDir() string {
	if runtime.GOOS == "windows" {
		if profile := os.Getenv("USERPROFILE"); profile != "" {
			return profile
		}
	}
	home, _ := os.UserHomeDir()
	return home
}

// Root returns the log root for a provider.
func (s Scanner) Root(provider string) string {
	if provider == Codex {
		return s.CodexRoot
	}
	return s.ClaudeRoot
}

type fileEntry struct {
	path    string
	modTime time.Time
}

// files lists session logs newest first.
func (s Scanner) files(provider string) ([]string, error) {
	pattern := filepath.Join(s.Root(provider), "*", "*.jsonl")
	if provider == Codex {
		pattern = filepath.Join(s.Root(provider), "*", "*", "*", "rollout-*.jsonl")
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	entries := make([]fileEntry, 0, len(matches))
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			continue
		}
		entries = append(entries, fileEntry{path: match, modTime: info.ModTime()})
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].modTime.After(entries[b].modTime) })
	paths := make([]string, len(entries))
	for index, item := range entries {
		paths[index] = item.path
	}
	return paths, nil
}

// FindCutOff returns the newest session whose last real event is a limit
// error, ignoring cut-offs written before since. A zero since disables the bound.
func (s Scanner) FindCutOff(provider string, since time.Time) (Session, bool, error) {
	paths, err := s.files(provider)
	if err != nil {
		return Session{}, false, err
	}
	for _, path := range paths {
		found, cut, err := s.inspectSession(provider, path)
		if err != nil || !cut {
			continue
		}
		if !since.IsZero() && !found.CutOffAt.After(since) {
			continue
		}
		return found, true, nil
	}
	return Session{}, false, nil
}
