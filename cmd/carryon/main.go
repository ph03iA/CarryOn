// Command carryon keeps Claude Code and Codex work going through usage limits.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ph03iA/CarryOn/internal/config"
	"github.com/ph03iA/CarryOn/internal/engine"
	"github.com/ph03iA/CarryOn/internal/logx"
	"github.com/ph03iA/CarryOn/internal/store"
)

// version is stamped at build time with -ldflags.
var version = "0.1.0"

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		printHelp()
		return 0
	}
	command := args[0]
	rest := args[1:]
	switch command {
	case "help", "-h", "--help":
		printHelp()
		return 0
	case "version", "--version":
		fmt.Println("carryon", version)
		return 0
	}

	paths, err := config.ResolvePaths()
	if err != nil {
		fmt.Fprintln(os.Stderr, "carryon:", err)
		return 1
	}
	if err := config.EnsureDir(paths.Dir); err != nil {
		fmt.Fprintln(os.Stderr, "carryon:", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	handlers := map[string]func(context.Context, config.Paths, []string) error{
		"init":      runInit,
		"watch":     runWatch,
		"log":       runLog,
		"status":    runStatus,
		"decisions": runDecisions,
		"simulate":  runSimulate,
		"service":   runService,
		"reset":     runReset,
	}
	handler, ok := handlers[command]
	if !ok {
		fmt.Fprintf(os.Stderr, "carryon: unknown command %q; run carryon help\n", command)
		return 1
	}
	if err := handler(ctx, paths, rest); err != nil {
		fmt.Fprintln(os.Stderr, "carryon:", err)
		return 1
	}
	return 0
}

// openEngine builds the engine with real providers, decider, runner and sender.
func openEngine(paths config.Paths, log *logx.Logger) (*engine.Engine, *store.DB, config.Config, error) {
	cfg, err := config.Load(paths.Config)
	if err != nil {
		return nil, nil, config.Config{}, err
	}
	secrets, err := config.LoadSecrets(paths.Dir)
	if err != nil {
		return nil, nil, config.Config{}, err
	}
	db, err := store.Open(paths.Database)
	if err != nil {
		return nil, nil, config.Config{}, err
	}
	built, err := assemble(db, cfg, secrets, log)
	if err != nil {
		db.Close()
		return nil, nil, config.Config{}, err
	}
	return built, db, cfg, nil
}

func printHelp() {
	fmt.Print(`CarryOn keeps Claude Code and Codex work going through usage limits.

Usage:
  carryon init [--providers claude|codex|both]  create the state folder and config
  carryon watch [--once] [--interval 60s]
  carryon status [--json]
  carryon decisions [--limit 20]
  carryon simulate [codex]              replay the newest session, labelled SIMULATED
  carryon simulate --session <id>       replay a specific session
  carryon log                           follow the log until Ctrl+C
  carryon reset [--yes]                 delete the state database
  carryon service install|uninstall|status
  carryon version

CarryOn reads plan usage every minute without sending a prompt. When a limit
comes back it emails you, finds the session that was cut off, waits for a fresh
reading, and asks Gemini whether to continue. A finish email follows each
resumed run. The providers setting limits what the watcher reads; a limit recorded for a disabled tool is closed without resuming. Config and keys are kept when you reset state.
`)
}

// logFile opens the shared log file in append mode.
func logFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
}

// parseFlag reads "--name value" or "--name=value" style arguments.
func parseFlag(args []string, name string) (string, bool) {
	for index, arg := range args {
		if value, found := strings.CutPrefix(arg, name+"="); found {
			return value, true
		}
		if arg == name && index+1 < len(args) {
			return args[index+1], true
		}
	}
	return "", false
}

// hasFlag reports whether a boolean flag is present.
func hasFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == name {
			return true
		}
	}
	return false
}

var errMissingSession = errors.New("--session <id> is required")
