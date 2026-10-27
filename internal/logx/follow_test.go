package logx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *syncBuffer) Write(chunk []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, chunk...)
	return len(chunk), nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

func TestFollowStreamsExistingAndNewLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "carryon.log")
	if err := os.WriteFile(path, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var output syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Follow(ctx, path, &output, 10*time.Millisecond) }()

	waitFor(t, &output, "one\n")
	appendLine(t, path, "two\n")
	waitFor(t, &output, "two\n")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("follow: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("follow did not stop on cancel")
	}
}

func TestFollowWaitsForCompleteLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "carryon.log")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	var output syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Follow(ctx, path, &output, 10*time.Millisecond) }()

	appendLine(t, path, "par")
	time.Sleep(80 * time.Millisecond)
	if strings.Contains(output.String(), "par") {
		t.Fatalf("partial line was written: %q", output.String())
	}
	appendLine(t, path, "tial\n")
	waitFor(t, &output, "partial\n")
}

func TestFollowWaitsForMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "carryon.log")
	var output syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Follow(ctx, path, &output, 10*time.Millisecond) }()

	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(path, []byte("created\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, &output, "created\n")
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, output *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(output.String(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q; got %q", want, output.String())
}
