package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ph03iA/CarryOn/internal/config"
)

func TestInitDoesNotCreateEnvFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CARRYON_HOME", dir)

	if code := run([]string{"init", "--email", "you@example.com"}); code != 0 {
		t.Fatalf("init exited with code %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
		t.Fatalf("init must leave .env creation to the user; stat error: %v", err)
	}
	cfg, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Recipient != "you@example.com" {
		t.Fatalf("init did not save the recipient: %q", cfg.Recipient)
	}
}

func TestInitPreservesExistingEnvFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CARRYON_HOME", dir)
	path := filepath.Join(dir, ".env")
	const content = "# Keep this file unchanged.\nGEMINI_API_KEY=test-key\nCUSTOM_VALUE=keep\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if code := run([]string{"init", "--email", "you@example.com"}); code != 0 {
		t.Fatalf("init exited with code %d", code)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Fatal("init changed the existing .env file")
	}
}
