package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/ph03iA/CarryOn/internal/config"
	"github.com/ph03iA/CarryOn/internal/decision"
	"github.com/ph03iA/CarryOn/internal/engine"
	"github.com/ph03iA/CarryOn/internal/logx"
	"github.com/ph03iA/CarryOn/internal/mail"
	"github.com/ph03iA/CarryOn/internal/quota"
	"github.com/ph03iA/CarryOn/internal/resume"
	"github.com/ph03iA/CarryOn/internal/session"
	"github.com/ph03iA/CarryOn/internal/single"
	"github.com/ph03iA/CarryOn/internal/store"
)

// assemble wires the real components onto a store.
func assemble(db *store.DB, cfg config.Config, secrets config.Secrets, log *logx.Logger) (*engine.Engine, error) {
	var decider decision.Decider
	if gemini, err := decision.NewGemini(context.Background(), secrets.Gemini, cfg.GeminiModel); err != nil {
		decider = decision.Unavailable{Err: err}
	} else {
		decider = gemini
	}
	built := &engine.Engine{
		DB:      db,
		Config:  cfg,
		Scanner: session.NewScanner(),
		Decider: decider,
		Runner: resume.Runner{
			ClaudeBin: cfg.ClaudeBin,
			CodexBin:  cfg.CodexBin,
			Timeout:   time.Duration(cfg.TimeoutMinutes) * time.Minute,
		},
		Log: log,
		Providers: []quota.Provider{
			quota.Codex{Binary: cfg.CodexBin},
			quota.Claude{Binary: cfg.ClaudeBin},
		},
	}
	if secrets.Resend != "" {
		built.Sender = mail.NewResend(secrets.Resend)
	}
	return built, nil
}

func runInit(_ context.Context, paths config.Paths, _ []string) error {
	if err := config.EnsureDir(paths.Dir); err != nil {
		return err
	}
	cfg, err := config.Load(paths.Config)
	if err != nil {
		return err
	}
	if err := config.Save(paths.Config, cfg); err != nil {
		return err
	}
	if err := writeEnvTemplate(paths.Env); err != nil {
		return err
	}
	fmt.Printf("CarryOn is set up in %s\n\n", paths.Dir)
	fmt.Println("Next steps:")
	fmt.Println("  1. Put your keys in", paths.Env)
	fmt.Println("     RESEND_API_KEY=re_...")
	fmt.Println("     GEMINI_API_KEY=...")
	fmt.Printf("  2. Emails go to %s from %s\n", cfg.Recipient, cfg.Sender)
	fmt.Println("  3. Run: carryon watch")
	fmt.Println("  4. Install the logon task: carryon service install")
	return nil
}

func writeEnvTemplate(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	content := "# CarryOn secrets. Never commit this file.\nRESEND_API_KEY=\nGEMINI_API_KEY=\n"
	return os.WriteFile(path, []byte(content), 0o600)
}

func runWatch(ctx context.Context, paths config.Paths, args []string) error {
	lock, err := single.Acquire(single.Name)
	if err != nil {
		return err
	}
	defer lock.Release()
	file, err := logFile(paths.Log)
	if err != nil {
		return err
	}
	defer file.Close()
	log := logx.New(file)
	built, db, cfg, err := openEngine(paths, log)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := built.Reconcile(ctx); err != nil {
		return err
	}
	interval := time.Duration(cfg.PollSeconds) * time.Second
	if value, ok := parseFlag(args, "--interval"); ok {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("invalid --interval: %w", err)
		}
		interval = parsed
	}
	if hasFlag(args, "--once") {
		return built.Step(ctx)
	}
	log.Infof("watching every %s; state in %s", interval, paths.Dir)
	return built.Run(ctx, interval)
}

func runStatus(_ context.Context, paths config.Paths, args []string) error {
	log := logx.Discard()
	built, db, cfg, err := openEngine(paths, log)
	if err != nil {
		return err
	}
	defer db.Close()
	status, err := built.LoadStatus(context.Background())
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		data, err := json.MarshalIndent(status, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}
	fmt.Printf("CarryOn %s — state in %s\n", version, paths.Dir)
	fmt.Printf("Recipient: %s  Sender: %s  Model: %s\n\n", cfg.Recipient, cfg.Sender, cfg.GeminiModel)
	printIncidents(status.Incidents)
	printResumes(status.Resumes)
	printEmails(status.Emails)
	return nil
}

func printIncidents(incidents []store.Incident) {
	fmt.Printf("Incidents (%d)\n", len(incidents))
	for _, incident := range incidents {
		fmt.Printf("  %-6s %-10s %-22s reset %s  %s\n", incident.Provider,
			incident.WindowLabel, incident.State,
			incident.ResetAt.Local().Format("2006-01-02 15:04"), incident.Reason)
	}
	fmt.Println()
}

func printResumes(resumes []store.Resume) {
	fmt.Printf("Resumes (%d)\n", len(resumes))
	for _, item := range resumes {
		fmt.Printf("  %-6s %-12s attempts %d  exit %d  %s\n", item.Provider,
			item.State, item.Attempts, item.ExitCode, shortID(item.SessionID))
	}
	fmt.Println()
}

func printEmails(emails []store.Email) {
	fmt.Printf("Emails (%d)\n", len(emails))
	for _, email := range emails {
		fmt.Printf("  %-16s %-8s attempts %d  %s\n", email.Kind, email.State, email.Attempts, email.Subject)
	}
}

func runDecisions(_ context.Context, paths config.Paths, args []string) error {
	log := logx.Discard()
	built, db, _, err := openEngine(paths, log)
	if err != nil {
		return err
	}
	defer db.Close()
	limit := 20
	if value, ok := parseFlag(args, "--limit"); ok {
		fmt.Sscanf(value, "%d", &limit)
	}
	decisions, err := built.DB.Decisions(context.Background(), limit)
	if err != nil {
		return err
	}
	for _, item := range decisions {
		fmt.Printf("%s  %-5s %-12s %s\n", item.CreatedAt.Local().Format("2006-01-02 15:04"),
			item.Stage, item.Action, item.Reason)
		if item.Prompt != "" {
			fmt.Printf("       prompt: %s\n", item.Prompt)
		}
	}
	return nil
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}
