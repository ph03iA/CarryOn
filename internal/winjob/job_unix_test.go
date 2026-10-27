//go:build !windows

package winjob

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	switch os.Getenv("CARRYON_FAKE_MODE") {
	case "stdin":
		cwd, _ := os.Getwd()
		io.ReadAll(os.Stdin)
		os.WriteFile(filepath.Join(cwd, "stdin-closed.txt"), []byte("eof"), 0o600)
		return
	case "heartbeat":
		cwd, _ := os.Getwd()
		path := filepath.Join(cwd, "heartbeat.txt")
		for {
			os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339Nano)), 0o600)
			time.Sleep(40 * time.Millisecond)
		}
	}
	os.Exit(m.Run())
}

func fakeEnv(mode string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if entry == "CARRYON_FAKE_MODE="+mode {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "CARRYON_FAKE_MODE="+mode)
}

func TestCloseStopsTheProcessGroup(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	process, err := Start(Spec{Path: exe, Dir: dir, Env: fakeEnv("heartbeat")})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	path := filepath.Join(dir, "heartbeat.txt")
	waitForFile(t, path)

	process.Close()
	assertStopped(t, path)
}

func TestTimeoutTerminatesTheProcessGroup(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	result, err := Run(context.Background(), Spec{
		Path: exe, Dir: dir, Env: fakeEnv("heartbeat"), Timeout: 600 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !result.TimedOut {
		t.Fatal("expected a timeout")
	}
	assertStopped(t, filepath.Join(dir, "heartbeat.txt"))
}

func TestCloseStdinAfterStart(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	result, err := Run(context.Background(), Spec{
		Path: exe, Dir: dir, Env: fakeEnv("stdin"), CloseStdin: true, Timeout: 3 * time.Second,
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
		t.Fatal("process survived the group kill")
	}
}
