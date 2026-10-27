package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ph03iA/CarryOn/internal/config"
)

func TestConfirmReset(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{"YES\n", true},
		{"n\n", false},
		{"\n", false},
		{"", false},
		{"no\n", false},
	}
	for _, testCase := range cases {
		var out bytes.Buffer
		got, err := confirmReset(strings.NewReader(testCase.input), &out)
		if err != nil {
			t.Fatalf("input %q: %v", testCase.input, err)
		}
		if got != testCase.want {
			t.Fatalf("input %q: got %v want %v", testCase.input, got, testCase.want)
		}
		if out.String() != resetPrompt {
			t.Fatalf("input %q: prompt %q", testCase.input, out.String())
		}
	}
}

func TestResetStateKeepsConfigAndKeys(t *testing.T) {
	dir := t.TempDir()
	paths := config.Paths{
		Dir:      dir,
		Config:   filepath.Join(dir, "config.json"),
		Database: filepath.Join(dir, "state.db"),
		Log:      filepath.Join(dir, "carryon.log"),
		Env:      filepath.Join(dir, ".env"),
	}
	files := map[string]string{
		paths.Config:                    "{}",
		paths.Env:                       "RESEND_API_KEY=re_x\n",
		paths.Database:                  "db",
		paths.Database + "-wal":         "wal",
		paths.Database + "-shm":         "shm",
		filepath.Join(dir, "other.txt"): "keep",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := resetState(paths); err != nil {
		t.Fatalf("reset: %v", err)
	}
	for _, gone := range []string{paths.Database, paths.Database + "-wal", paths.Database + "-shm"} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s still exists", gone)
		}
	}
	for _, kept := range []string{paths.Config, paths.Env, filepath.Join(dir, "other.txt")} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("%s was removed: %v", kept, err)
		}
	}
}

func TestResetStateMissingFilesIsFine(t *testing.T) {
	paths := config.Paths{Database: filepath.Join(t.TempDir(), "state.db")}
	if err := resetState(paths); err != nil {
		t.Fatalf("reset missing: %v", err)
	}
}
