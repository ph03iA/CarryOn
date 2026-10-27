// Package config loads CarryOn settings and the two API keys it needs.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	DefaultRecipient    = "prajwalchahande7@gmail.com"
	DefaultSender       = "onboarding@resend.dev"
	DefaultGeminiModel  = "gemini-3.5-flash-lite"
	DefaultProviders    = "both"
	DefaultPollSeconds  = 60
	DefaultTimeoutMins  = 360
	DefaultMaxResumes   = 2
	DefaultTailChars    = 16000
	DefaultPromptChars  = 500
	envResendKey        = "RESEND_API_KEY"
	envGeminiKey        = "GEMINI_API_KEY"
	stateDirEnv         = "CARRYON_HOME"
	configFileName      = "config.json"
	envFileName         = ".env"
	stateDirPermissions = 0o700
	filePermissions     = 0o600
)

// Config is the persisted, user-editable configuration.
type Config struct {
	Providers      string `json:"providers"`
	Recipient      string `json:"recipient"`
	Sender         string `json:"sender"`
	GeminiModel    string `json:"gemini_model"`
	ClaudeBin      string `json:"claude_bin,omitempty"`
	CodexBin       string `json:"codex_bin,omitempty"`
	PollSeconds    int    `json:"poll_seconds"`
	TimeoutMinutes int    `json:"resume_timeout_minutes"`
	MaxResumes     int    `json:"max_resumes"`
	TailChars      int    `json:"session_tail_chars"`
	PromptChars    int    `json:"decision_prompt_chars"`
}

// Defaults returns a configuration with every field set to a working value.
func Defaults() Config {
	return Config{
		Providers:      DefaultProviders,
		Recipient:      DefaultRecipient,
		Sender:         DefaultSender,
		GeminiModel:    DefaultGeminiModel,
		PollSeconds:    DefaultPollSeconds,
		TimeoutMinutes: DefaultTimeoutMins,
		MaxResumes:     DefaultMaxResumes,
		TailChars:      DefaultTailChars,
		PromptChars:    DefaultPromptChars,
	}
}

// Normalize fills empty fields with defaults and clamps unsafe values.
func (c Config) Normalize() Config {
	defaults := Defaults()
	fill(&c.Providers, defaults.Providers)
	fill(&c.Recipient, defaults.Recipient)
	fill(&c.Sender, defaults.Sender)
	fill(&c.GeminiModel, defaults.GeminiModel)
	clampInt(&c.PollSeconds, 15, 3600, defaults.PollSeconds)
	clampInt(&c.TimeoutMinutes, 1, 24*60, defaults.TimeoutMinutes)
	clampInt(&c.MaxResumes, 1, 2, defaults.MaxResumes)
	clampInt(&c.TailChars, 1000, 200000, defaults.TailChars)
	clampInt(&c.PromptChars, 50, DefaultPromptChars, defaults.PromptChars)
	return c
}

func fill(target *string, fallback string) {
	if strings.TrimSpace(*target) == "" {
		*target = fallback
	}
}

func clampInt(target *int, low, high, fallback int) {
	if *target == 0 {
		*target = fallback
		return
	}
	if *target < low {
		*target = low
	}
	if *target > high {
		*target = high
	}
}

// Paths describes where CarryOn keeps its state.
type Paths struct {
	Dir      string
	Config   string
	Database string
	Log      string
	Env      string
}

// ResolvePaths returns the state directory and the files inside it.
func ResolvePaths() (Paths, error) {
	dir := strings.TrimSpace(os.Getenv(stateDirEnv))
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return Paths{}, fmt.Errorf("locate config directory: %w", err)
		}
		dir = filepath.Join(base, "CarryOn")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve state directory: %w", err)
	}
	return Paths{
		Dir:      absolute,
		Config:   filepath.Join(absolute, configFileName),
		Database: filepath.Join(absolute, "state.db"),
		Log:      filepath.Join(absolute, "carryon.log"),
		Env:      filepath.Join(absolute, envFileName),
	}, nil
}

// EnsureDir creates the state directory with restrictive permissions.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, stateDirPermissions); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("state path must be a real directory")
	}
	return nil
}

// Load reads config.json, returning defaults when it does not exist.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Defaults(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var loaded Config
	if err := json.Unmarshal(data, &loaded); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	providers, err := NormalizeProviders(loaded.Providers)
	if err != nil {
		return Config{}, err
	}
	loaded.Providers = providers
	return loaded.Normalize(), nil
}

// Save writes config.json atomically with restrictive permissions.
func Save(path string, cfg Config) error {
	providers, err := NormalizeProviders(cfg.Providers)
	if err != nil {
		return err
	}
	cfg.Providers = providers
	data, err := json.MarshalIndent(cfg.Normalize(), "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), stateDirPermissions); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), filePermissions); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		os.Remove(temporary)
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

// NormalizeProviders accepts claude, codex, or both.
func NormalizeProviders(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "both":
		return "both", nil
	case "claude":
		return "claude", nil
	case "codex":
		return "codex", nil
	default:
		return "", fmt.Errorf("providers must be claude, codex, or both, got %q", value)
	}
}

// Watches reports whether a provider is enabled in this configuration.
func (c Config) Watches(provider string) bool {
	providers, err := NormalizeProviders(c.Providers)
	if err != nil {
		return true
	}
	return providers == "both" || providers == provider
}

// Secrets holds the two API keys CarryOn needs. They are never persisted.
type Secrets struct {
	Resend string
	Gemini string
}

// LoadSecrets reads the keys from the environment first, then from the .env
// file in the state directory, then from the working directory.
func LoadSecrets(stateDir string) (Secrets, error) {
	var secrets Secrets
	fillSecret(&secrets.Resend, os.Getenv(envResendKey))
	fillSecret(&secrets.Gemini, os.Getenv(envGeminiKey))
	for _, path := range envCandidates(stateDir) {
		values, err := ReadEnvFile(path)
		if err != nil {
			return Secrets{}, err
		}
		fillSecret(&secrets.Resend, values[envResendKey])
		fillSecret(&secrets.Gemini, values[envGeminiKey])
	}
	return secrets, nil
}

func envCandidates(stateDir string) []string {
	seen := map[string]bool{}
	var paths []string
	for _, candidate := range []string{filepath.Join(stateDir, envFileName), envFileName} {
		absolute, err := filepath.Abs(candidate)
		if err != nil || seen[absolute] {
			continue
		}
		seen[absolute] = true
		paths = append(paths, absolute)
	}
	return paths
}

func fillSecret(target *string, value string) {
	if *target == "" {
		*target = strings.TrimSpace(value)
	}
}

// RedactedName reports whether an environment variable must be withheld from
// child processes. It covers both keys and the SDK's alternate spellings.
func RedactedName(name string) bool {
	upper := strings.ToUpper(name)
	switch upper {
	case envResendKey, envGeminiKey, "GOOGLE_API_KEY", "GOOGLE_GENAI_USE_ENTERPRISE":
		return true
	}
	return strings.HasPrefix(upper, "RESEND_") || strings.HasPrefix(upper, "GEMINI_") ||
		strings.HasPrefix(upper, "GOOGLE_GENAI_")
}

// ChildEnvironment returns the current environment with every secret removed,
// so a spawned Claude or Codex process can never inherit an API key.
func ChildEnvironment() []string {
	parent := os.Environ()
	cleaned := make([]string, 0, len(parent))
	for _, entry := range parent {
		name, _, found := strings.Cut(entry, "=")
		if found && RedactedName(name) {
			continue
		}
		cleaned = append(cleaned, entry)
	}
	return cleaned
}
