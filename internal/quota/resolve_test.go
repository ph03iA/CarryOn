package quota

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveBinaryUsesPath(t *testing.T) {
	dir := t.TempDir()
	name := "carryon-fake-cli"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := ResolveBinary("carryon-fake-cli", "", nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("want %s, got %s", want, got)
	}
}

func TestResolveBinaryHonorsConfiguredPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveBinary("claude", path, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("want %s, got %s", want, got)
	}
}

func TestResolveBinaryReportsMissing(t *testing.T) {
	if _, err := ResolveBinary("carryon-definitely-missing", "", nil); err == nil {
		t.Fatal("want an error for a missing binary")
	}
}
