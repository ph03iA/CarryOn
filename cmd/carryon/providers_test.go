package main

import (
	"testing"

	"github.com/ph03iA/CarryOn/internal/config"
)

func TestProvidersAfterInit(t *testing.T) {
	cfg := config.Defaults()
	cfg.Providers = "codex"

	kept, err := providersAfterInit(cfg, nil)
	if err != nil || kept.Providers != "codex" {
		t.Fatalf("no flag should keep codex: %+v %v", kept, err)
	}
	changed, err := providersAfterInit(cfg, []string{"--providers", "claude"})
	if err != nil || changed.Providers != "claude" {
		t.Fatalf("flag should set claude: %+v %v", changed, err)
	}
	inline, err := providersAfterInit(cfg, []string{"--providers=both"})
	if err != nil || inline.Providers != "both" {
		t.Fatalf("inline flag should set both: %+v %v", inline, err)
	}
	if _, err := providersAfterInit(cfg, []string{"--providers", "gemini"}); err == nil {
		t.Fatal("want an error for an unknown provider")
	}
}

func TestWatchLabel(t *testing.T) {
	cases := map[string]string{
		"claude": "claude",
		"codex":  "codex",
		"both":   "both",
	}
	for value, want := range cases {
		if got := watchLabel(value); got != want {
			t.Fatalf("%s: got %q want %q", value, got, want)
		}
	}
}

func TestRecipientAfterInit(t *testing.T) {
	cfg := config.Defaults()
	cfg.Recipient = "old@example.com"

	kept, err := recipientAfterInit(cfg, nil)
	if err != nil || kept.Recipient != "old@example.com" {
		t.Fatalf("no flag should keep the recipient: %+v %v", kept, err)
	}
	changed, err := recipientAfterInit(cfg, []string{"--email", "you@example.com"})
	if err != nil || changed.Recipient != "you@example.com" {
		t.Fatalf("flag should set the recipient: %+v %v", changed, err)
	}
	if _, err := recipientAfterInit(cfg, []string{"--email=not-an-email"}); err == nil {
		t.Fatal("want an error for an invalid address")
	}
	if _, err := recipientAfterInit(cfg, []string{"--email", "Name <you@example.com>"}); err == nil {
		t.Fatal("want an error for a display name")
	}
}
