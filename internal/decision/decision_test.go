package decision

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/genai"
)

type fakeTransport struct {
	status int
	body   string
	seen   string
}

func (f *fakeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	payload, _ := io.ReadAll(request.Body)
	f.seen = string(payload)
	status := f.status
	if status == 0 {
		status = http.StatusOK
	}
	body := f.body
	if body == "" {
		body = `{"candidates":[{"content":{"role":"model","parts":[{"text":"{\"action\":\"resume\",\"reason\":\"work remains\",\"prompt\":\"add the missing test\"}"}]},"finishReason":"STOP"}]}`
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Request:    request,
	}, nil
}

func newTestGemini(t *testing.T, transport *fakeTransport) *Gemini {
	t.Helper()
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:     "test-key",
		Backend:    genai.BackendGeminiAPI,
		HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return newWithClient(client, "gemini-flash-latest")
}

func preRequest() Request {
	return Request{
		Stage:      "pre",
		Provider:   "claude",
		Cwd:        `C:\work\demo`,
		SessionID:  "1111",
		LimitLabel: "5-hour",
		Attempts:   0,
		Transcript: `assistant: I'll fix the failing test.`,
	}
}

func TestGeminiReturnsValidatedResume(t *testing.T) {
	transport := &fakeTransport{}
	gemini := newTestGemini(t, transport)
	result, err := gemini.Decide(context.Background(), preRequest())
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if result.Action != ActionResume || result.Prompt != "add the missing test" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.InputChars == 0 || result.Model != "gemini-flash-latest" {
		t.Fatalf("missing metadata: %+v", result)
	}
	if !strings.Contains(transport.seen, "untrusted DATA") {
		t.Fatal("system instruction was not sent")
	}
	if !strings.Contains(transport.seen, "BEGIN UNTRUSTED SESSION EXCERPT") {
		t.Fatal("transcript delimiters were not sent")
	}
}

func TestGeminiRejectsInvalidJSON(t *testing.T) {
	transport := &fakeTransport{body: `{"candidates":[{"content":{"parts":[{"text":"not json"}]}}]}`}
	gemini := newTestGemini(t, transport)
	if _, err := gemini.Decide(context.Background(), preRequest()); err == nil {
		t.Fatal("want an error for invalid JSON")
	}
}

func TestGeminiRejectsUnknownAction(t *testing.T) {
	transport := &fakeTransport{body: `{"candidates":[{"content":{"parts":[{"text":"{\"action\":\"party\",\"reason\":\"x\",\"prompt\":\"\"}"}]}}]}`}
	gemini := newTestGemini(t, transport)
	if _, err := gemini.Decide(context.Background(), preRequest()); err == nil {
		t.Fatal("want an error for an unknown action")
	}
}

func TestGeminiRejectsMissingPrompt(t *testing.T) {
	transport := &fakeTransport{body: `{"candidates":[{"content":{"parts":[{"text":"{\"action\":\"resume\",\"reason\":\"x\",\"prompt\":\"\"}"}]}}]}`}
	gemini := newTestGemini(t, transport)
	if _, err := gemini.Decide(context.Background(), preRequest()); err == nil {
		t.Fatal("want an error when a resume has no prompt")
	}
}

func TestGeminiCapsPromptAt500(t *testing.T) {
	long := strings.Repeat("step ", 200)
	transport := &fakeTransport{body: `{"candidates":[{"content":{"parts":[{"text":"{\"action\":\"resume\",\"reason\":\"x\",\"prompt\":\"` + long + `\"}"}]}}]}`}
	gemini := newTestGemini(t, transport)
	result, err := gemini.Decide(context.Background(), preRequest())
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if len([]rune(result.Prompt)) > 500 {
		t.Fatalf("prompt not capped: %d runes", len([]rune(result.Prompt)))
	}
}

func TestBuildPromptRedactsSecretsAndKeepsTail(t *testing.T) {
	request := preRequest()
	request.TailChars = 120
	request.Transcript = "old start\n" + strings.Repeat("x", 200) + "\nkey sk-abcdefghijklmnopqrstuvwx and AKIAABCDEFGHIJKLMNOP and github_pat_ABCDEFGHIJKLMNOPQRSTUVWX"
	text, removed := BuildPrompt(request)
	if removed < 3 {
		t.Fatalf("want at least 3 redactions, got %d", removed)
	}
	for _, canary := range []string{"sk-abcdefghijklmnopqrstuvwx", "AKIAABCDEFGHIJKLMNOP", "github_pat_ABCDEFGHIJKLMNOPQRSTUVWX"} {
		if strings.Contains(text, canary) {
			t.Fatalf("secret leaked: %s", canary)
		}
	}
	if strings.Contains(text, "old start") {
		t.Fatal("head should be trimmed away")
	}
	if !strings.Contains(text, "[REDACTED]") {
		t.Fatal("missing redaction marker")
	}
}

func TestValidateClearsPromptForSkip(t *testing.T) {
	result, err := validate("pre", Result{Action: ActionSkipDone, Reason: "already finished", Prompt: "ignored"}, 500)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if result.Prompt != "" {
		t.Fatal("skip decisions must not carry a prompt")
	}
}

func TestResumePromptStartsWithFixedInstruction(t *testing.T) {
	prompt := ResumePrompt("run the failing test")
	if !strings.HasPrefix(prompt, FixedInstruction) {
		t.Fatalf("prompt does not start with the fixed instruction: %s", prompt)
	}
}
