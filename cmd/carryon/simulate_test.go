package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSimulateRequest(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		provider string
		session  string
	}{
		{name: "no arguments", args: nil, provider: "claude", session: ""},
		{name: "codex positional", args: []string{"codex"}, provider: "codex", session: ""},
		{name: "claude positional", args: []string{"claude"}, provider: "claude", session: ""},
		{name: "session only", args: []string{"--session", "abc"}, provider: "", session: "abc"},
		{name: "provider and session", args: []string{"--provider", "codex", "--session", "abc"}, provider: "codex", session: "abc"},
		{name: "positional and session", args: []string{"codex", "--session", "abc"}, provider: "codex", session: "abc"},
	}
	for _, testCase := range cases {
		provider, session := simulateRequest(testCase.args)
		if provider != testCase.provider || session != testCase.session {
			t.Fatalf("%s: got (%q, %q) want (%q, %q)", testCase.name, provider, session, testCase.provider, testCase.session)
		}
	}
}

func TestConfirmSimulate(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"n\n", false},
		{"\n", false},
		{"", false},
	}
	for _, testCase := range cases {
		var out bytes.Buffer
		got, err := confirmSimulate(strings.NewReader(testCase.input), &out, "Claude Code", "abc", `C:\work\demo`)
		if err != nil {
			t.Fatalf("input %q: %v", testCase.input, err)
		}
		if got != testCase.want {
			t.Fatalf("input %q: got %v want %v", testCase.input, got, testCase.want)
		}
		want := `Simulate Claude Code session abc in C:\work\demo? [y/N] `
		if out.String() != want {
			t.Fatalf("input %q: prompt %q", testCase.input, out.String())
		}
	}
}
