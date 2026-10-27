package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ph03iA/CarryOn/internal/config"
	"github.com/ph03iA/CarryOn/internal/logx"
	"github.com/ph03iA/CarryOn/internal/service"
	"github.com/ph03iA/CarryOn/internal/session"
)

func runService(ctx context.Context, paths config.Paths, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: carryon service install|uninstall|status")
	}
	switch args[0] {
	case "install":
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		absolute, err := filepath.Abs(exe)
		if err != nil {
			return err
		}
		note, err := service.Install(ctx, absolute, paths.Dir)
		if err != nil {
			return err
		}
		fmt.Println(note)
		fmt.Println("It starts at every sign-in and restarts on failure.")
		fmt.Printf("Logs: %s\n", paths.Log)
		return nil
	case "uninstall":
		if err := service.Uninstall(ctx); err != nil {
			return err
		}
		fmt.Println("Removed the CarryOn logon task.")
		return nil
	case "status":
		state, err := service.Status(ctx)
		if err != nil {
			return err
		}
		fmt.Println(state)
		return nil
	default:
		return fmt.Errorf("unknown service command %q", args[0])
	}
}

func runSimulate(ctx context.Context, paths config.Paths, args []string) error {
	provider, sessionID := simulateRequest(args)
	scanner := session.NewScanner()

	var found session.Session
	var err error
	if sessionID == "" {
		found, err = scanner.Newest(provider)
	} else {
		found, err = findSession(scanner, provider, sessionID)
	}
	if err != nil {
		return err
	}

	file, err := logFile(paths.Log)
	if err != nil {
		return err
	}
	defer file.Close()
	log := logx.New(file)
	built, db, _, err := openEngine(paths, log)
	if err != nil {
		return err
	}
	defer db.Close()
	built.Synchronous = true

	folder := found.Cwd
	if folder == "" {
		folder = "unknown folder"
	}
	fmt.Printf("Simulating %s session %s in %s\n", displayProvider(found.Provider), found.ID, folder)
	fmt.Println("SIMULATED run: this spends real Gemini and Claude/Codex usage and sends real email.")
	fmt.Println("It fakes only the usage limit and its reset.")
	return built.Simulate(ctx, found.Provider, found.ID)
}

// simulateRequest resolves the provider and an optional explicit session id.
func simulateRequest(args []string) (provider, sessionID string) {
	provider, _ = parseFlag(args, "--provider")
	sessionID, _ = parseFlag(args, "--session")
	for _, arg := range args {
		if arg == session.Claude || arg == session.Codex {
			provider = arg
			break
		}
	}
	if provider == "" && sessionID == "" {
		provider = session.Claude
	}
	return provider, sessionID
}

// findSession looks up an explicit session id, trying both providers when the
// provider was not given.
func findSession(scanner session.Scanner, provider, id string) (session.Session, error) {
	if provider != "" {
		return scanner.FindByID(provider, id)
	}
	if found, err := scanner.FindByID(session.Claude, id); err == nil {
		return found, nil
	}
	return scanner.FindByID(session.Codex, id)
}

func displayProvider(provider string) string {
	switch provider {
	case session.Claude:
		return "Claude Code"
	case session.Codex:
		return "Codex"
	default:
		return provider
	}
}
