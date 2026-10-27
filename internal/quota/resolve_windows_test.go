//go:build windows

package quota

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveBinaryRejectsShim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.cmd")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveBinary("claude", path, nil); err == nil {
		t.Fatal("want an error for a shell shim")
	}
}
