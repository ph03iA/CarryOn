package mail

import (
	"fmt"
	"strings"
	"time"
)

// Event kinds CarryOn emails.
const (
	KindLimitHit      = "limit_hit"
	KindDecisionSkip  = "decision_skip"
	KindDecisionBlock = "decision_block"
	KindResumeStart   = "resume_start"
	KindResumeFinish  = "resume_finish"
)

// Event is everything a template may render.
type Event struct {
	Kind        string
	Provider    string
	WindowLabel string
	ResetAt     time.Time
	SessionID   string
	SessionCwd  string
	Action      string
	Reason      string
	Prompt      string
	ExitCode    int
	TimedOut    bool
	LastMessage string
	Files       []string
	TestOutput  string
	Simulated   bool
	Now         time.Time
}

// Render builds the subject and plain-text body for one event.
func Render(event Event) (string, string) {
	now := event.Now
	if now.IsZero() {
		now = time.Now()
	}
	subject := subjectFor(event)
	body := strings.Join(bodyFor(event, now), "\n")
	if event.Simulated {
		subject = "[SIMULATED] " + subject
		body = "SIMULATED — no real usage limit was hit.\n\n" + body
	}
	return subject, body
}

func subjectFor(event Event) string {
	provider := providerName(event.Provider)
	switch event.Kind {
	case KindLimitHit:
		return fmt.Sprintf("%s %s limit hit — resets %s", provider, event.WindowLabel, localTime(event.ResetAt))
	case KindDecisionSkip:
		return fmt.Sprintf("CarryOn skipped the %s session", provider)
	case KindDecisionBlock:
		return fmt.Sprintf("CarryOn needs you: the %s session was blocked", provider)
	case KindResumeStart:
		return fmt.Sprintf("CarryOn resumed the %s session", provider)
	case KindResumeFinish:
		return fmt.Sprintf("CarryOn finished the %s session", provider)
	default:
		return "CarryOn notification"
	}
}

func bodyFor(event Event, now time.Time) []string {
	lines := []string{fmt.Sprintf("%s (%s)", providerName(event.Provider), displaySession(event.SessionID))}
	switch event.Kind {
	case KindLimitHit:
		lines = append(lines,
			fmt.Sprintf("Limit: %s", event.WindowLabel),
			fmt.Sprintf("Resets: %s (%s)", localTime(event.ResetAt), utcTime(event.ResetAt)),
		)
		if event.Reason != "" {
			lines = append(lines, event.Reason)
		}
	case KindDecisionSkip, KindDecisionBlock:
		lines = append(lines, fmt.Sprintf("Decision: %s", event.Action), "Reason: "+event.Reason)
	case KindResumeStart:
		lines = append(lines, "Decision: resume", "Reason: "+event.Reason)
		if event.Prompt != "" {
			lines = append(lines, "Prompt: "+event.Prompt)
		}
	case KindResumeFinish:
		lines = append(lines, "Decision: "+event.Action, "Reason: "+event.Reason,
			fmt.Sprintf("Exit status: %d", event.ExitCode))
		if event.TimedOut {
			lines = append(lines, "The run hit CarryOn's time limit and was stopped.")
		}
	}
	if event.SessionCwd != "" {
		lines = append(lines, "Folder: "+event.SessionCwd)
	}
	if event.LastMessage != "" {
		lines = append(lines, "", "Last agent message:", event.LastMessage)
	}
	if len(event.Files) > 0 {
		lines = append(lines, "", "Files changed:")
		for _, file := range event.Files {
			lines = append(lines, "  "+file)
		}
	}
	if event.TestOutput != "" {
		lines = append(lines, "", "Test output:", event.TestOutput)
	}
	lines = append(lines, "", fmt.Sprintf("Sent by CarryOn at %s.", localTime(now)))
	return lines
}

func providerName(provider string) string {
	switch provider {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "":
		return "CarryOn"
	default:
		return provider
	}
}

func localTime(value time.Time) string {
	if value.IsZero() {
		return "unknown"
	}
	return value.Local().Format("2006-01-02 3:04 PM MST")
}

func utcTime(value time.Time) string {
	if value.IsZero() {
		return "unknown"
	}
	return value.UTC().Format("2006-01-02 15:04 UTC")
}

// displaySession names a session in email, or says none was found.
func displaySession(sessionID string) string {
	if sessionID == "" {
		return "no cut-off session found"
	}
	return sessionID
}
