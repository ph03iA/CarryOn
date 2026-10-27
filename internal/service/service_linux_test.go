//go:build linux

package service

import (
	"strings"
	"testing"
)

func TestUnitRunsWatchAndRestarts(t *testing.T) {
	value := unit("/usr/local/bin/carryon", "/home/dev/.config/CarryOn", "")
	required := []string{
		`ExecStart="/usr/local/bin/carryon" watch`,
		`WorkingDirectory="/home/dev/.config/CarryOn"`,
		"Restart=on-failure",
		"WantedBy=default.target",
	}
	for _, want := range required {
		if !strings.Contains(value, want) {
			t.Fatalf("unit missing %q:\n%s", want, value)
		}
	}
}

func TestUnitQuotesSpaces(t *testing.T) {
	value := unit(`/opt/my tools/carryon`, `/home/dev/my state`, "")
	if !strings.Contains(value, `ExecStart="/opt/my tools/carryon" watch`) {
		t.Fatalf("executable not quoted:\n%s", value)
	}
	if !strings.Contains(value, `WorkingDirectory="/home/dev/my state"`) {
		t.Fatalf("working directory not quoted:\n%s", value)
	}
}

func TestUnitEscapesSystemdSyntax(t *testing.T) {
	value := unit(`/opt/100%/carryon`, `/tmp/"quoted"`, "")
	if !strings.Contains(value, `100%%`) {
		t.Fatalf("percent not escaped:\n%s", value)
	}
	if !strings.Contains(value, `\"quoted\"`) {
		t.Fatalf("quote not escaped:\n%s", value)
	}
}

func TestUnitRecordsSearchPath(t *testing.T) {
	value := unit("/usr/local/bin/carryon", "/tmp/state", "/home/dev/.local/bin:/usr/bin")
	if !strings.Contains(value, `Environment="PATH=/home/dev/.local/bin:/usr/bin"`) {
		t.Fatalf("unit does not record PATH:\n%s", value)
	}
	if strings.Contains(unit("/usr/local/bin/carryon", "/tmp/state", ""), "Environment=") {
		t.Fatal("an empty PATH must not add Environment=")
	}
}
