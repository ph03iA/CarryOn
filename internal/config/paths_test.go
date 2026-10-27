package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePathsUsesCarryOnHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(stateDirEnv, dir)

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if paths.Dir != dir {
		t.Fatalf("want %s, got %s", dir, paths.Dir)
	}
	want := map[string]string{
		"config":   filepath.Join(dir, configFileName),
		"database": filepath.Join(dir, "state.db"),
		"log":      filepath.Join(dir, "carryon.log"),
		"env":      filepath.Join(dir, envFileName),
	}
	got := map[string]string{
		"config": paths.Config, "database": paths.Database, "log": paths.Log, "env": paths.Env,
	}
	for name, path := range want {
		if got[name] != path {
			t.Fatalf("%s: want %s, got %s", name, path, got[name])
		}
	}
}

func TestResolvePathsDefaultsToUserConfig(t *testing.T) {
	t.Setenv(stateDirEnv, "")
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("user config dir: %v", err)
	}

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := filepath.Join(base, "CarryOn")
	if paths.Dir != want {
		t.Fatalf("want %s, got %s", want, paths.Dir)
	}
}

func TestResolvePathsMakesRelativeHomeAbsolute(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(stateDirEnv, "state")

	paths, err := ResolvePaths()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want, err := filepath.Abs("state")
	if err != nil {
		t.Fatal(err)
	}
	if paths.Dir != want {
		t.Fatalf("want %s, got %s", want, paths.Dir)
	}
}
