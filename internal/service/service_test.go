package service

import (
	"strings"
	"testing"
)

func TestScriptRegistersAResilientLogonTask(t *testing.T) {
	script := Script(`C:\Tools\carryon.exe`, `C:\Users\dev\AppData\Roaming\CarryOn`, `DOMAIN\dev`)
	required := []string{
		"Register-ScheduledTask",
		"-TaskName 'CarryOn'",
		`-Execute 'C:\Tools\carryon.exe'`,
		"-Argument 'watch'",
		`-WorkingDirectory 'C:\Users\dev\AppData\Roaming\CarryOn'`,
		"-AtLogOn -User 'DOMAIN\\dev'",
		"-RestartCount 999",
		"-RestartInterval (New-TimeSpan -Minutes 1)",
		"-MultipleInstances IgnoreNew",
		"-ExecutionTimeLimit ([TimeSpan]::Zero)",
		"-StartWhenAvailable",
		"-LogonType S4U",
		"-Principal $principal",
	}
	for _, want := range required {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
}

func TestScriptEscapesSingleQuotes(t *testing.T) {
	script := Script(`C:\it's\carryon.exe`, `C:\work`, `DOMAIN\o'brien`)
	if !strings.Contains(script, `C:\it''s\carryon.exe`) {
		t.Fatalf("path quote not escaped:\n%s", script)
	}
	if !strings.Contains(script, `DOMAIN\o''brien`) {
		t.Fatalf("user quote not escaped:\n%s", script)
	}
}

func TestUninstallTargetsTheSameTask(t *testing.T) {
	script := "Unregister-ScheduledTask -TaskName 'CarryOn'"
	if !strings.Contains(script, TaskName) {
		t.Fatal("task name constant drifted")
	}
}

func TestInstallRejectsEmptyPaths(t *testing.T) {
	if _, err := Install(nil, "", ""); err == nil {
		t.Fatal("want an error without paths")
	}
}

func TestInteractiveScriptHasNoS4U(t *testing.T) {
	script := InteractiveScript(`C:\Tools\carryon.exe`, `C:\work`, `DOMAIN\dev`)
	if strings.Contains(script, "S4U") {
		t.Fatalf("interactive fallback must not claim S4U:\n%s", script)
	}
}
