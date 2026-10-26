package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ph03iA/CarryOn/internal/config"
	"github.com/ph03iA/CarryOn/internal/logx"
	"github.com/ph03iA/CarryOn/internal/service"
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
		if err := service.Install(ctx, absolute, paths.Dir); err != nil {
			return err
		}
		fmt.Println("Installed the CarryOn logon task. It starts at every sign-in and restarts on failure.")
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
	sessionID, ok := parseFlag(args, "--session")
	if !ok || sessionID == "" {
		return errMissingSession
	}
	provider, _ := parseFlag(args, "--provider")
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
	fmt.Println("SIMULATED run: this spends real Gemini and Claude/Codex usage and sends real email.")
	fmt.Println("It fakes only the usage limit and its reset.")
	return built.Simulate(ctx, provider, sessionID)
}
