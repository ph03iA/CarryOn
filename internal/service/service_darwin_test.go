//go:build darwin

package service

import (
	"strings"
	"testing"
)

func TestPlistRunsWatchAtLogin(t *testing.T) {
	value := plist("/Applications/CarryOn", "/Users/dev/Library/Application Support/CarryOn", "")
	required := []string{
		"<string>dev.carryon</string>",
		"<string>/Applications/CarryOn</string>",
		"<string>watch</string>",
		"<string>/Users/dev/Library/Application Support/CarryOn</string>",
		"<key>RunAtLoad</key>\n\t<true/>",
		"<key>KeepAlive</key>\n\t<true/>",
	}
	for _, want := range required {
		if !strings.Contains(value, want) {
			t.Fatalf("plist missing %q:\n%s", want, value)
		}
	}
}

func TestPlistEscapesXML(t *testing.T) {
	value := plist(`/tmp/a&b`, `/tmp/<work>`, "")
	if !strings.Contains(value, `/tmp/a&amp;b`) {
		t.Fatalf("path ampersand not escaped:\n%s", value)
	}
	if !strings.Contains(value, `/tmp/&lt;work&gt;`) {
		t.Fatalf("working directory not escaped:\n%s", value)
	}
}

func TestPlistRecordsSearchPath(t *testing.T) {
	value := plist("/usr/local/bin/carryon", "/tmp/state", "/Users/dev/.local/bin:/opt/homebrew/bin:/usr/bin")
	want := "<key>PATH</key>\n\t\t<string>/Users/dev/.local/bin:/opt/homebrew/bin:/usr/bin</string>"
	if !strings.Contains(value, "<key>EnvironmentVariables</key>") || !strings.Contains(value, want) {
		t.Fatalf("plist does not record PATH:\n%s", value)
	}
	if strings.Contains(plist("/usr/local/bin/carryon", "/tmp/state", ""), "EnvironmentVariables") {
		t.Fatal("an empty PATH must not add EnvironmentVariables")
	}
}
