package mail

import (
	"fmt"
	"strings"
	"time"

	"github.com/ph03iA/CarryOn/internal/redact"
)

// Event kinds CarryOn emails.
const (
	KindBack         = "back"
	KindNeedsYou     = "needs_you"
	KindResumeFinish = "resume_finish"
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
	Summary     string
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
	return redact.Text(subject).Text, redact.Text(body).Text
}

func subjectFor(event Event) string {
	provider := providerName(event.Provider)
	switch event.Kind {
	case KindBack:
		return fmt.Sprintf("CarryOn: %s is back", provider)
	case KindNeedsYou:
		return fmt.Sprintf("CarryOn needs you: %s reset was never confirmed", provider)
	case KindResumeFinish:
		return fmt.Sprintf("CarryOn finished the %s session", provider)
	default:
		return "CarryOn notification"
	}
}

func bodyFor(event Event, now time.Time) []string {
	if event.Kind == KindBack {
		return backBody(event)
	}
	lines := []string{fmt.Sprintf("%s (%s)", providerName(event.Provider), displaySession(event.SessionID))}
	switch event.Kind {
	case KindNeedsYou:
		lines = append(lines,
			fmt.Sprintf("Limit: %s", event.WindowLabel),
			fmt.Sprintf("Reset: %s (%s)", localTime(event.ResetAt), utcTime(event.ResetAt)),
			"The reset was not confirmed with a fresh reading within 24 hours, so CarryOn stopped waiting.",
		)
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

// backBody leads with the provider being back, then says what CarryOn did.
func backBody(event Event) []string {
	lines := []string{fmt.Sprintf("%s is back — %s", providerName(event.Provider), event.Summary)}
	if !event.ResetAt.IsZero() {
		lines = append(lines, "Reset: "+localTime(event.ResetAt)+" ("+utcTime(event.ResetAt)+")")
	}
	if event.SessionCwd != "" {
		lines = append(lines, "Folder: "+event.SessionCwd)
	}
	if event.Prompt != "" {
		lines = append(lines, "Prompt: "+event.Prompt)
	}
	if event.Reason != "" {
		lines = append(lines, "Reason: "+event.Reason)
	}
	lines = append(lines, "", fmt.Sprintf("Sent by CarryOn at %s.", localTime(event.Now)))
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

func displaySession(sessionID string) string {
	if sessionID == "" {
		return "no cut-off session found"
	}
	return sessionID
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
