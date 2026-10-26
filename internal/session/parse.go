package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var uuidPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// event is the normalized view of one log record.
type event struct {
	real       bool
	limitError bool
	cwd        string
	text       string
	role       string
	at         time.Time
}

var claudeRealTypes = map[string]bool{"user": true, "assistant": true, "result": true}

var codexEventTypes = map[string]bool{
	"task_started": true, "task_complete": true, "turn_aborted": true,
	"error": true, "stream_error": true, "item_completed": true, "token_count": true, "agent_message": true,
}

var codexResponseTypes = map[string]bool{
	"message": true, "agent_message": true, "reasoning": true,
	"function_call": true, "function_call_output": true,
	"custom_tool_call": true, "custom_tool_call_output": true,
}

// parseEvent normalizes a record and reports whether it was understood.
func parseEvent(provider string, data []byte) (event, bool) {
	if provider == Codex {
		return parseCodexEvent(data)
	}
	return parseClaudeEvent(data)
}

type claudeRecord struct {
	Type              string `json:"type"`
	IsAPIErrorMessage bool   `json:"isApiErrorMessage"`
	IsError           bool   `json:"is_error"`
	Cwd               string `json:"cwd"`
	Timestamp         string `json:"timestamp"`
	Result            string `json:"result"`
	Message           *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

func parseClaudeEvent(data []byte) (event, bool) {
	var record claudeRecord
	if json.Unmarshal(data, &record) != nil {
		return event{}, false
	}
	if !claudeRealTypes[record.Type] {
		return event{}, false
	}
	text := record.Result
	if record.Message != nil {
		texts, extras := claudeParts(record.Message.Content)
		text = strings.Join(append(texts, extras...), "\n")
	}
	return event{
		real:       true,
		limitError: claudeLimitError(record, text),
		cwd:        record.Cwd,
		text:       text,
		role:       record.Type,
		at:         parseTimestamp(record.Timestamp),
	}, true
}

// claudeParts accepts both a plain string and a block list, and keeps tool
// names and inputs so the decision prompt can see what the agent was doing.
func claudeParts(raw json.RawMessage) (texts, extras []string) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	if trimmed[0] == '"' {
		var text string
		if json.Unmarshal(trimmed, &text) == nil && text != "" {
			texts = append(texts, text)
		}
		return texts, nil
	}
	var blocks []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(trimmed, &blocks) != nil {
		return nil, nil
	}
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				texts = append(texts, block.Text)
			}
		case "tool_use":
			extras = append(extras, fmt.Sprintf("[tool %s] %s", block.Name, compact(block.Input)))
		}
	}
	return texts, extras
}

func claudeLimitError(record claudeRecord, text string) bool {
	if record.Type == "assistant" && record.IsAPIErrorMessage {
		return matchesLimit(text)
	}
	if record.Type == "result" && record.IsError {
		return matchesLimit(text)
	}
	return false
}

type codexRecord struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
Payload struct {
		Type             string          `json:"type"`
		ID               string          `json:"id"`
		Role             string          `json:"role"`
		Cwd              string          `json:"cwd"`
		Message          string          `json:"message"`
		Reason           string          `json:"reason"`
		LastAgentMessage string          `json:"last_agent_message"`
		Name             string          `json:"name"`
		Arguments        string          `json:"arguments"`
		Input            string          `json:"input"`
		Content          json.RawMessage `json:"content"`
		RateLimits       *struct {
			RateLimitReachedType *string `json:"rate_limit_reached_type"`
		} `json:"rate_limits"`
	} `json:"payload"`
}

func parseCodexEvent(data []byte) (event, bool) {
	var record codexRecord
	if json.Unmarshal(data, &record) != nil {
		return event{}, false
	}
	if record.Type == "session_meta" {
		return event{cwd: record.Payload.Cwd}, true
	}
	if !codexReal(record) {
		return event{}, false
	}
	text := codexText(record)
	return event{
		real:       true,
		limitError: codexLimitError(record),
		cwd:        record.Payload.Cwd,
		text:       text,
		role:       codexRole(record),
		at:         parseTimestamp(record.Timestamp),
	}, true
}

// codexText collects message text, tool names and tool arguments.
func codexText(record codexRecord) string {
	parts := make([]string, 0, 4)
	appendNonEmpty(&parts, record.Payload.Message)
	parts = append(parts, codexContentText(record.Payload.Content)...)
	if tool := codexTool(record); tool != "" {
		parts = append(parts, tool)
	}
	return strings.Join(parts, "\n")
}

func codexTool(record codexRecord) string {
	if record.Payload.Type != "function_call" && record.Payload.Type != "custom_tool_call" {
		return ""
	}
	argument := record.Payload.Arguments
	if argument == "" {
		argument = record.Payload.Input
	}
	if record.Payload.Name == "" && argument == "" {
		return ""
	}
	return fmt.Sprintf("[tool %s] %s", record.Payload.Name, compactText(argument))
}

// codexContentText reads content blocks such as [{type,input_text,text}].
func codexContentText(raw json.RawMessage) []string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	if trimmed[0] == '"' {
		var text string
		if json.Unmarshal(trimmed, &text) == nil && text != "" {
			return []string{text}
		}
		return nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(trimmed, &blocks) != nil {
		return nil
	}
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Text != "" {
			texts = append(texts, block.Text)
		}
	}
	return texts
}

func appendNonEmpty(parts *[]string, value string) {
	if strings.TrimSpace(value) != "" {
		*parts = append(*parts, value)
	}
}

func compact(raw json.RawMessage) string { return compactText(string(raw)) }

func compactText(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= 300 {
		return trimmed
	}
	return trimmed[:300] + "…"
}

func codexReal(record codexRecord) bool {
	if record.Type == "event_msg" {
		return codexEventTypes[record.Payload.Type]
	}
	if record.Type == "response_item" {
		return codexResponseTypes[record.Payload.Type]
	}
	return false
}

func codexLimitError(record codexRecord) bool {
	if record.Type == "response_item" {
		return false
	}
	switch record.Payload.Type {
	case "error", "stream_error":
		return matchesLimit(record.Payload.Message)
	case "turn_aborted":
		return matchesLimit(record.Payload.Message + " " + record.Payload.Reason)
	case "token_count":
		reached := record.Payload.RateLimits
		return reached != nil && reached.RateLimitReachedType != nil &&
			strings.TrimSpace(*reached.RateLimitReachedType) != ""
	}
	return false
}

func parseTimestamp(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

// inspectSession reads one log and reports whether its newest real event is a
// usage-limit error.
func (s Scanner) inspectSession(provider, path string) (Session, bool, error) {
	lines, err := readTail(path, s.MaxBytes)
	if err != nil {
		return Session{}, false, err
	}
	found := Session{Provider: provider, Path: path}
	info, err := os.Stat(path)
	if err == nil {
		found.ModTime = info.ModTime().UTC()
	}
	found.ID = sessionID(provider, path, lines)
	if provider == Codex {
		found.Cwd = codexHeadCwd(path)
	}
	cut := false
	for index := len(lines) - 1; index >= 0; index-- {
		event, ok := parseEvent(provider, lines[index].Data)
		if !ok {
			continue
		}
		if event.cwd != "" && found.Cwd == "" {
			found.Cwd = event.cwd
		}
		if !event.real {
			continue
		}
		if found.CutOffAt.IsZero() {
			found.CutOffAt = event.at
			found.ResumeFrom = lines[index].End
			cut = event.limitError
			continue
		}
		if found.LastMessage == "" {
			found.LastMessage = summarize(assistantText(provider, lines[index].Data))
		}
	}
	return found, cut, nil
}

// sessionID derives the conversation id from the filename or session_meta.
func sessionID(provider, path string, lines []line) string {
	if provider != Codex {
		return strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}
	if id := uuidFromName(path); id != "" {
		return id
	}
	for _, item := range lines {
		var record codexRecord
		if json.Unmarshal(item.Data, &record) != nil {
			continue
		}
		if record.Type == "session_meta" && record.Payload.ID != "" {
			return record.Payload.ID
		}
	}
	return strings.TrimSuffix(filepath.Base(path), ".jsonl")
}

// summarize keeps the last part of a message short enough for an email.
func summarize(text string) string {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) <= 400 {
		return trimmed
	}
	return "…" + trimmed[len(trimmed)-400:]
}

// Continued reports whether the session gained a real event after the cut-off.
func (s Scanner) Continued(found Session) (bool, error) {
	if found.ResumeFrom <= 0 {
		return false, nil
	}
	lines, err := readFrom(found.Path, found.ResumeFrom)
	if err != nil {
		return false, err
	}
	for _, item := range lines {
		event, ok := parseEvent(found.Provider, item.Data)
		if ok && event.real {
			return true, nil
		}
	}
	return false, nil
}

// FindByID loads one session by its provider and conversation id.
func (s Scanner) FindByID(provider, id string) (Session, error) {
	paths, err := s.files(provider)
	if err != nil {
		return Session{}, err
	}
	for _, path := range paths {
		lines, err := readTail(path, s.MaxBytes)
		if err != nil {
			continue
		}
		if sessionID(provider, path, lines) != id {
			continue
		}
		found, _, err := s.inspectSession(provider, path)
		return found, err
	}
	return Session{}, ErrNotFound
}

// LastMessage returns the last assistant message recorded in a session.
func (s Scanner) LastMessage(found Session) string {
	lines, err := readTail(found.Path, s.MaxBytes)
	if err != nil {
		return ""
	}
	for index := len(lines) - 1; index >= 0; index-- {
		if text := assistantText(found.Provider, lines[index].Data); text != "" {
			return summarize(text)
		}
	}
	return ""
}

func assistantText(provider string, data []byte) string {
	if provider == Codex {
		return codexAssistantText(data)
	}
	var record claudeRecord
	if json.Unmarshal(data, &record) != nil || record.Type != "assistant" || record.IsAPIErrorMessage {
		return ""
	}
	if record.Message == nil {
		return ""
	}
	texts, _ := claudeParts(record.Message.Content)
	return strings.Join(texts, "\n")
}

// codexRole names the speaker for a transcript line.
func codexRole(record codexRecord) string {
	if record.Payload.Role != "" {
		return record.Payload.Role
	}
	return record.Payload.Type
}

// Transcript returns a redaction-ready excerpt of the session, keeping the end.
func (s Scanner) Transcript(found Session, maxChars int) string {
	lines, err := readTail(found.Path, s.MaxBytes)
	if err != nil {
		return ""
	}
	var builder strings.Builder
	for _, item := range lines {
		event, ok := parseEvent(found.Provider, item.Data)
		if !ok || !event.real || strings.TrimSpace(event.text) == "" {
			continue
		}
		builder.WriteString(event.role)
		builder.WriteString(": ")
		builder.WriteString(limitChars(event.text, 2000))
		builder.WriteString("\n")
	}
	return tailChars(builder.String(), maxChars)
}

func limitChars(text string, max int) string {
	if max <= 0 || len(text) <= max {
		return text
	}
	return text[:max] + "…"
}

func tailChars(text string, max int) string {
	if max <= 0 || len(text) <= max {
		return text
	}
	return "…" + text[len(text)-max:]
}

// codexAssistantText returns the agent's own words, not tool traffic.
func codexAssistantText(data []byte) string {
	var record codexRecord
	if json.Unmarshal(data, &record) != nil {
		return ""
	}
	if record.Payload.Type == "task_complete" && record.Payload.LastAgentMessage != "" {
		return record.Payload.LastAgentMessage
	}
	if record.Type == "event_msg" && record.Payload.Type == "agent_message" {
		return record.Payload.Message
	}
	if record.Type == "response_item" && record.Payload.Role == "assistant" {
		return strings.Join(codexContentText(record.Payload.Content), "\n")
	}
	return ""
}

// uuidFromName returns the last UUID in a Codex rollout filename.
func uuidFromName(path string) string {
	matches := uuidPattern.FindAllString(filepath.Base(path), -1)
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1]
}

// codexHeadCwd reads the folder from session_meta, which lives in the first
// line and is lost when a large log is only read from the tail.
func codexHeadCwd(path string) string {
	head, err := readHead(path, 64<<10)
	if err != nil {
		return ""
	}
	for _, item := range head {
		event, ok := parseEvent(Codex, item.Data)
		if ok && event.cwd != "" {
			return event.cwd
		}
	}
	return ""
}
