package main

import "testing"

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
