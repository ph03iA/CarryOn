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
