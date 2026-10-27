package resume

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain lets the test binary act as a fake Claude or Codex CLI.
func TestMain(m *testing.M) {
	if mode := os.Getenv("CARRYON_FAKE_MODE"); mode != "" {
		os.Exit(runFake(mode))
	}
	os.Exit(m.Run())
}

func runFake(mode string) int {
	cwd, _ := os.Getwd()
	switch mode {
	case "claude":
		os.WriteFile(filepath.Join(cwd, "resumed.txt"), []byte("ok"), 0o600)
		fmt.Println(`{"type":"result","result":"Finished the task and wrote resumed.txt"}`)
	case "claude_stderr":
		fmt.Println(`{"type":"result","result":"Result from stdout"}`)
		fmt.Fprintln(os.Stderr, "warning: no stdin data received")
	case "claude_edit":
		file, _ := os.OpenFile(filepath.Join(cwd, "tracked.txt"), os.O_APPEND|os.O_WRONLY, 0o600)
		file.WriteString("run edit\n")
		file.Close()
		os.WriteFile(filepath.Join(cwd, "resumed.txt"), []byte("ok"), 0o600)
		fmt.Println("Edited tracked.txt")
	case "codex":
		writeLastMessage()
		os.WriteFile(filepath.Join(cwd, "codex-output.txt"), []byte("ok"), 0o600)
		fmt.Println("$ go test ./...")
		fmt.Println("ok  example  0.10s")
	case "env":
		dump := strings.Join(os.Environ(), "\n")
		os.WriteFile(filepath.Join(cwd, "env.txt"), []byte(dump), 0o600)
	case "slow":
		heartbeat(cwd)
	case "fail":
		fmt.Fprintln(os.Stderr, "boom")
		return 3
	}
	return 0
}

func writeLastMessage() {
	for index, arg := range os.Args {
		if arg == "-o" && index+1 < len(os.Args) {
			os.WriteFile(os.Args[index+1], []byte("Done: wrote codex-output.txt"), 0o600)
		}
	}
}

// heartbeat proves the process is alive until it is killed.
func heartbeat(cwd string) {
	path := filepath.Join(cwd, "heartbeat.txt")
	for {
		os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339Nano)), 0o600)
		time.Sleep(50 * time.Millisecond)
	}
}
