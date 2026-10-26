package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadEnvFileHandlesQuotesAndComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "\ufeff# comment\r\nRESEND_API_KEY=re_plain\r\nGEMINI_API_KEY='gm_quoted'\r\nexport OTHER=\"value # not a comment\"\r\nTRAILING=abc # trailing comment\r\n\r\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	values, err := ReadEnvFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if values["RESEND_API_KEY"] != "re_plain" {
		t.Fatalf("unexpected resend key: %q", values["RESEND_API_KEY"])
	}
	if values["GEMINI_API_KEY"] != "gm_quoted" {
		t.Fatalf("unexpected gemini key: %q", values["GEMINI_API_KEY"])
	}
	if values["OTHER"] != "value # not a comment" {
		t.Fatalf("unexpected other: %q", values["OTHER"])
	}
	if values["TRAILING"] != "abc" {
		t.Fatalf("unexpected trailing: %q", values["TRAILING"])
	}
}

func TestReadEnvFileRejectsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("NO_EQUALS\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEnvFile(path); err == nil {
		t.Fatal("want an error for a line without =")
	}
}

func TestReadEnvFileMissingIsNotAnError(t *testing.T) {
	values, err := ReadEnvFile(filepath.Join(t.TempDir(), "missing"))
	if err != nil || len(values) != 0 {
		t.Fatalf("values=%v err=%v", values, err)
	}
}

func TestChildEnvironmentStripsSecrets(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "re_secret")
	t.Setenv("GEMINI_API_KEY", "gm_secret")
	t.Setenv("GOOGLE_API_KEY", "goog_secret")
	t.Setenv("KEEP_ME", "visible")
	env := ChildEnvironment()
	joined := strings.Join(env, "\n")
	for _, secret := range []string{"re_secret", "gm_secret", "goog_secret"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("secret leaked: %s", secret)
		}
	}
	if !strings.Contains(joined, "KEEP_ME=visible") {
		t.Fatal("ordinary variables must be kept")
	}
}

func TestRedactedName(t *testing.T) {
	for _, name := range []string{"RESEND_API_KEY", "GEMINI_API_KEY", "GOOGLE_GENAI_USE_ENTERPRISE", "RESEND_FROM"} {
		if !RedactedName(name) {
			t.Fatalf("%s should be withheld", name)
		}
	}
	if RedactedName("PATH") {
		t.Fatal("PATH must not be withheld")
	}
}

func TestNormalizeFillsAndClamps(t *testing.T) {
	cfg := Config{PollSeconds: 1, MaxResumes: 9, PromptChars: 5000}
	normalized := cfg.Normalize()
	if normalized.Recipient != DefaultRecipient || normalized.Sender != DefaultSender {
		t.Fatalf("defaults not applied: %+v", normalized)
	}
	if normalized.PollSeconds != 15 {
		t.Fatalf("poll not clamped: %d", normalized.PollSeconds)
	}
	if normalized.MaxResumes != 2 {
		t.Fatalf("max resumes not clamped: %d", normalized.MaxResumes)
	}
	if normalized.PromptChars != DefaultPromptChars {
		t.Fatalf("prompt chars not clamped: %d", normalized.PromptChars)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := Defaults()
	cfg.GeminiModel = "gemini-2.5-flash"
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.GeminiModel != "gemini-2.5-flash" || loaded.Recipient != DefaultRecipient {
		t.Fatalf("unexpected config: %+v", loaded)
	}
}

func TestLoadMissingReturnsDefaults(t *testing.T) {
	loaded, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil || loaded.Recipient != DefaultRecipient {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
}

func TestLoadSecretsPrefersEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("RESEND_API_KEY=file_value\nGEMINI_API_KEY=file_gemini\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RESEND_API_KEY", "env_value")
	t.Chdir(dir)
	secrets, err := LoadSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if secrets.Resend != "env_value" {
		t.Fatalf("environment should win: %q", secrets.Resend)
	}
	if secrets.Gemini != "file_gemini" {
		t.Fatalf("file value should be used: %q", secrets.Gemini)
	}
}

func TestDefaultRecipientHasTheDoubleZero(t *testing.T) {
	if got := Defaults().Recipient; got != "prajwalchahande07@gmail.com" {
		t.Fatalf("default recipient is wrong: %s", got)
	}
}
