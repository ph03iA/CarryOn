//go:build windows

package winjob

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	mode := os.Getenv("CARRYON_FAKE_MODE")
	if mode == "leaky" {
		leakyChild()
		return
	}
	if mode == "sleeper" {
		time.Sleep(30 * time.Second)
		return
	}
	if mode == "stdin" {
		cwd, _ := os.Getwd()
		io.ReadAll(os.Stdin)
		os.WriteFile(filepath.Join(cwd, "stdin-closed.txt"), []byte("eof"), 0o600)
		return
	}
	if mode == "heartbeat" {
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

func leakyChild() {
	child := exec.Command(os.Args[0])
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "CARRYON_FAKE_MODE=") {
			env = append(env, entry)
		}
	}
	child.Env = append(env, "CARRYON_FAKE_MODE=sleeper")
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	_ = child.Start()
}

func leakyEnv() []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if entry == "CARRYON_FAKE_MODE=leaky" {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "CARRYON_FAKE_MODE=leaky")
}

func TestWaitDelayDoesNotFailTheRun(t *testing.T) {
	previous := waitDelay
	waitDelay = 200 * time.Millisecond
	defer func() { waitDelay = previous }()

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	result, err := Run(context.Background(), Spec{
		Path: exe, Dir: t.TempDir(), Env: leakyEnv(), Timeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.TimedOut {
		t.Fatal("a background child holding stdout must not consume the whole timeout")
	}
	if result.ExitCode != 0 {
		t.Fatalf("want exit 0 after WaitDelay, got %d", result.ExitCode)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatalf("run took %s", time.Since(started))
	}
}

func stdinEnv() []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if entry == "CARRYON_FAKE_MODE=stdin" {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "CARRYON_FAKE_MODE=stdin")
}

func TestCloseStdinAfterStart(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	result, err := Run(context.Background(), Spec{
		Path: exe, Dir: dir, Env: stdinEnv(), CloseStdin: true, Timeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.TimedOut {
		t.Fatal("child did not see stdin EOF")
	}
	if _, err := os.Stat(filepath.Join(dir, "stdin-closed.txt")); err != nil {
		t.Fatalf("child did not report EOF: %v", err)
	}
}
