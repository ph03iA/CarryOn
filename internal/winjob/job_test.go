//go:build windows

package winjob

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("CARRYON_FAKE_MODE"); mode == "heartbeat" {
		cwd, _ := os.Getwd()
		path := filepath.Join(cwd, "heartbeat.txt")
		for {
			os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339Nano)), 0o600)
			time.Sleep(40 * time.Millisecond)
		}
	}
	os.Exit(m.Run())
}

func heartbeatEnv() []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if entry == "CARRYON_FAKE_MODE=heartbeat" {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "CARRYON_FAKE_MODE=heartbeat")
}

func TestKillOnCloseStopsTheProcess(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	process, err := Start(Spec{Path: exe, Dir: dir, Env: heartbeatEnv()})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	path := filepath.Join(dir, "heartbeat.txt")
	waitForFile(t, path)

	// Close the job without waiting: kill-on-close must terminate the child.
	process.Close()
	assertStopped(t, path)
}

func TestTimeoutTerminatesTheJob(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	result, err := Run(context.Background(), Spec{
		Path: exe, Dir: dir, Env: heartbeatEnv(), Timeout: 600 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !result.TimedOut {
		t.Fatal("expected a timeout")
	}
	assertStopped(t, filepath.Join(dir, "heartbeat.txt"))
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	for range 100 {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("file was never created: %s", path)
}

func assertStopped(t *testing.T, path string) {
	t.Helper()
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("heartbeat missing: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	after, err := os.Stat(path)
	if err != nil {
		return
	}
	if !after.ModTime().Equal(before.ModTime()) {
		fmt.Println("heartbeat still moving; process survived")
		t.Fatal("process survived the job close")
	}
}
