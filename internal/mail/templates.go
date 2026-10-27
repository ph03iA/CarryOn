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
	KindBack         = "back"
	KindNeedsYou     = "needs_you"
	KindResumeFinish = "resume_finish"
)

// Event is everything a template may render.
type Event struct {
	Kind        string
	Provider    string
	SessionID   string
	SessionCwd  string
	Action      string
	Reason      string
	Prompt      string
	ExitCode    int
	TimedOut    bool
	LastMessage string
	Files       []string
	Simulated   bool
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
		switch event.Action {
		case "resume":
			return joinFolder(fmt.Sprintf("%s is back: resuming", provider), folder)
		case "none":
			return fmt.Sprintf("%s is back: nothing to resume", provider)
		default:
			return joinFolder(fmt.Sprintf("%s is back: skipped", provider), folder)
		}
	case KindResumeFinish:
		if folder == "" {
			return fmt.Sprintf("%s finished the session", provider)
		}
		return fmt.Sprintf("%s finished: %s", provider, folder)
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
	case KindResumeFinish:
		return finishLines(event)
	case KindNeedsYou:
		return needsYouLines(event)
	default:
		return nil
	}
}

func backLines(event Event) []line {
	provider := providerName(event.Provider)
	lines := []line{firstLine(provider+" is back.", backTail(event.Action))}
	addProject(&lines, event)
	if event.Action == "resume" {
		addField(&lines, "Next step", event.Prompt)
	}
	addField(&lines, "Why", event.Reason)
	if event.Action == "resume" && event.SessionID != "" {
		lines = append(lines, commandLine(event))
	}
	return lines
}

func backTail(action string) string {
	switch action {
	case "resume":
		return " CarryOn resumed your session."
	case "skip_done":
		return " Skipped: already done."
	case "skip_stuck":
		return " Skipped: looked stuck."
	case "skip_continued":
		return " Skipped: the session was already continued."
	case "skip_no_decision":
		return " Skipped: no Gemini decision."
	case "skip_error":
		return " Skipped: could not read the session log."
	default:
		return " Nothing to resume."
	}
}

func finishLines(event Event) []line {
	lines := []line{firstLine("CarryOn finished the session.", " Result: "+resultText(event)+".")}
	addProject(&lines, event)
	addField(&lines, "Why", event.Reason)
	addField(&lines, "Changed", changedList(event.Files))
	addField(&lines, "Agent said", clip(event.LastMessage, 200))
	if event.SessionID != "" {
		lines = append(lines, commandLine(event))
	}
	return lines
}

func needsYouLines(event Event) []line {
	provider := providerName(event.Provider)
	lines := []line{firstLine("CarryOn needs you.", " The "+provider+" reset was not confirmed within 24 hours.")}
	addProject(&lines, event)
	return lines
}

func resultText(event Event) string {
	word := "stopped"
	switch event.Action {
	case "done":
		word = "done"
	case "resume_again":
		word = "ran once more"
	case "could not start":
		word = "could not start"
	}
	if event.TimedOut {
		return word + " (timeout)"
	}
	if event.ExitCode != 0 {
		return fmt.Sprintf("%s (exit %d)", word, event.ExitCode)
	}
	return word
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

func changedList(files []string) string {
	cleaned := make([]string, 0, len(files))
	for _, file := range files {
		value := clean(file)
		if value != "" {
			cleaned = append(cleaned, value)
		}
	}
	if len(cleaned) == 0 {
		return ""
	}
	if len(cleaned) > 5 {
		extra := len(cleaned) - 5
		cleaned = append(cleaned[:5], fmt.Sprintf("+%d more", extra))
	}
	return strings.Join(cleaned, ", ")
}

// clip limits a value to max runes, including the ellipsis.
func clip(value string, max int) string {
	value = clean(value)
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
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
