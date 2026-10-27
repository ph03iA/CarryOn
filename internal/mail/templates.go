package mail

import (
	"fmt"
	"html"
	"path"
	"strings"

	"github.com/ph03iA/CarryOn/internal/redact"
)

// Event kinds CarryOn emails.
const (
	KindBack     = "back"
	KindNeedsYou = "needs_you"
)

// Event is everything a template may render.
type Event struct {
	Kind       string
	Provider   string
	SessionID  string
	SessionCwd string
	Action     string
	Reason     string
	Prompt     string
	Simulated  bool
}

type line struct {
	text string
	html string
}

// Render returns the subject, the plain-text fallback, and the minimal HTML.
func Render(event Event) (string, string, string) {
	subject := redact.Text(subjectFor(event)).Text
	if event.Simulated {
		subject = "[SIMULATED] " + subject
	}

	lines := bodyLines(event)
	textParts := make([]string, 0, len(lines))
	htmlParts := make([]string, 0, len(lines))
	for _, item := range lines {
		textParts = append(textParts, item.text)
		htmlParts = append(htmlParts, item.html)
	}

	text := strings.Join(textParts, "\n\n")
	markup := strings.Join(htmlParts, "\n")
	if event.Simulated {
		text = "Simulated run: no real limit was hit.\n\n" + text
		markup = "<p>Simulated run: no real limit was hit.</p>\n" + markup
	}
	return subject, text, markup
}

func subjectFor(event Event) string {
	provider := providerName(event.Provider)
	folder := folderName(event.SessionCwd)
	switch event.Kind {
	case KindBack:
		if event.Action == "resume" {
			return joinFolder(fmt.Sprintf("%s is back: resumed", provider), folder)
		}
		return fmt.Sprintf("%s is back: no resume needed", provider)
	case KindNeedsYou:
		return fmt.Sprintf("CarryOn needs you: %s reset not confirmed", provider)
	default:
		return "CarryOn notification"
	}
}

func joinFolder(prefix, folder string) string {
	if folder == "" {
		return prefix
	}
	return prefix + " " + folder
}

func bodyLines(event Event) []line {
	switch event.Kind {
	case KindBack:
		return backLines(event)
	case KindNeedsYou:
		return needsYouLines(event)
	default:
		return nil
	}
}

func backLines(event Event) []line {
	provider := providerName(event.Provider)
	if event.Action == "resume" {
		lines := []line{firstLine(provider+" is back.", " CarryOn resumed your session.")}
		addProject(&lines, event)
		addField(&lines, "Next step", event.Prompt)
		addField(&lines, "Why", event.Reason)
		if event.SessionID != "" {
			lines = append(lines, commandLine(event))
		}
		return lines
	}

	lines := []line{firstLine(provider+" is back.", " No resume was needed.")}
	addProject(&lines, event)
	addField(&lines, "Why", backReason(event))
	return lines
}

// backReason turns the decision into the plain words used in the email.
func backReason(event Event) string {
	switch event.Action {
	case "skip_done":
		return "the task was already done"
	case "none":
		return "no session was cut off"
	case "skip_continued":
		return "you already continued it"
	case "skip_stuck":
		return "the agent looked stuck"
	case "skip_no_decision":
		return "no Gemini decision"
	case "skip_error":
		return "the session log could not be read"
	default:
		if reason := clean(event.Reason); reason != "" {
			return reason
		}
		return "no resume was needed"
	}
}

func needsYouLines(event Event) []line {
	provider := providerName(event.Provider)
	lines := []line{firstLine("CarryOn needs you.", " The "+provider+" reset was not confirmed within 24 hours.")}
	addProject(&lines, event)
	return lines
}

func firstLine(bold, rest string) line {
	cleanBold := redact.Text(bold).Text
	cleanRest := redact.Text(rest).Text
	return line{
		text: cleanBold + cleanRest,
		html: "<p><b>" + html.EscapeString(cleanBold) + "</b>" + html.EscapeString(cleanRest) + "</p>",
	}
}

func addField(lines *[]line, label, value string) {
	value = clean(value)
	if value == "" {
		return
	}
	*lines = append(*lines, line{
		text: label + ": " + value,
		html: "<p><b>" + html.EscapeString(label) + ":</b> " + html.EscapeString(value) + "</p>",
	})
}

func addProject(lines *[]line, event Event) {
	addField(lines, "Project", folderName(event.SessionCwd))
}

func commandLine(event Event) line {
	command := commandFor(event.Provider, event.SessionID)
	return line{
		text: "Continue by hand: " + command,
		html: "<p>Continue by hand: <code>" + html.EscapeString(clean(command)) + "</code></p>",
	}
}

func commandFor(provider, sessionID string) string {
	switch provider {
	case "codex":
		return "codex exec resume " + sessionID
	case "claude":
		return "claude --resume " + sessionID
	default:
		return "resume " + sessionID
	}
}

func clean(value string) string {
	return redact.Text(strings.TrimSpace(value)).Text
}

func folderName(folder string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(folder), "\\", "/")
	if normalized == "" {
		return ""
	}
	base := path.Base(normalized)
	if base == "." || base == "/" {
		return ""
	}
	return base
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
