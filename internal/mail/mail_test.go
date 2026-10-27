package mail

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	demoFolder  = `C:\work\carryon-demo-a2`
	demoSession = "bc7a02f5-1234-5678-9abc-def012345678"
)

func backResume() (string, string, string) {
	return Render(Event{
		Kind: KindBack, Provider: "claude", Action: "resume",
		SessionID: demoSession, SessionCwd: demoFolder,
		Prompt: "write subtract and its test",
		Reason: "add is done, three functions remain",
	})
}

func TestResendSendsBothVersions(t *testing.T) {
	var seenKey, seenAuth string
	var seenBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seenKey = request.Header.Get("Idempotency-Key")
		seenAuth = request.Header.Get("Authorization")
		payload, _ := io.ReadAll(request.Body)
		json.Unmarshal(payload, &seenBody)
		writer.WriteHeader(http.StatusOK)
		writer.Write([]byte(`{"id":"abc"}`))
	}))
	defer server.Close()

	sender := &Resend{APIKey: "re_test", BaseURL: server.URL, Client: server.Client()}
	err := sender.Send(context.Background(), Message{
		EventKey: "back:abc", To: "me@example.com", From: "onboarding@resend.dev",
		Subject: "subject", Body: "plain version", HTML: "<p>html version</p>",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if seenKey != "carryon-back:abc" {
		t.Fatalf("unexpected idempotency key: %q", seenKey)
	}
	if seenAuth != "Bearer re_test" {
		t.Fatalf("unexpected auth header: %q", seenAuth)
	}
	if seenBody["text"] != "plain version" {
		t.Fatalf("text version missing: %+v", seenBody)
	}
	if seenBody["html"] != "<p>html version</p>" {
		t.Fatalf("html version missing: %+v", seenBody)
	}
}

func TestResendClassifiesRetryableFailures(t *testing.T) {
	cases := []struct {
		status    int
		retryable bool
	}{
		{http.StatusOK, false},
		{http.StatusBadRequest, false},
		{http.StatusTooManyRequests, true},
		{http.StatusInternalServerError, true},
		{http.StatusRequestTimeout, true},
	}
	for _, testCase := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(testCase.status)
		}))
		sender := &Resend{APIKey: "re_test", BaseURL: server.URL, Client: server.Client()}
		err := sender.Send(context.Background(), Message{EventKey: "k", To: "a@b.c", From: "x@y.z"})
		if testCase.status == http.StatusOK {
			if err != nil {
				t.Fatalf("status %d: unexpected error %v", testCase.status, err)
			}
			server.Close()
			continue
		}
		if err == nil {
			t.Fatalf("status %d: want an error", testCase.status)
		}
		if Retryable(err) != testCase.retryable {
			t.Fatalf("status %d: retryable=%v want %v", testCase.status, Retryable(err), testCase.retryable)
		}
		server.Close()
	}
}

func TestResendReadsRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Retry-After", "42")
		writer.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	sender := &Resend{APIKey: "re_test", BaseURL: server.URL, Client: server.Client()}
	err := sender.Send(context.Background(), Message{EventKey: "k", To: "a@b.c", From: "x@y.z"})
	if RetryAfter(err) != 42*time.Second {
		t.Fatalf("unexpected retry-after: %v", RetryAfter(err))
	}
}

func TestResendRequiresKey(t *testing.T) {
	sender := &Resend{}
	if err := sender.Send(context.Background(), Message{EventKey: "k"}); err == nil {
		t.Fatal("want an error without an API key")
	}
	if Retryable(&deliveryError{message: "x"}) {
		t.Fatal("a missing key must not be retried")
	}
}

func TestBackResumeRendersBothVersions(t *testing.T) {
	subject, text, markup := backResume()
	if subject != "Claude Code is back: resumed carryon-demo-a2" {
		t.Fatalf("unexpected subject: %s", subject)
	}
	wantText := "Claude Code is back. CarryOn resumed your session.\n\n" +
		"Project: carryon-demo-a2\n\n" +
		"Next step: write subtract and its test\n\n" +
		"Why: add is done, three functions remain\n\n" +
		"Continue by hand: claude --resume " + demoSession
	if text != wantText {
		t.Fatalf("unexpected text:\n%s", text)
	}
	wantHTML := "<p><b>Claude Code is back.</b> CarryOn resumed your session.</p>\n" +
		"<p><b>Project:</b> carryon-demo-a2</p>\n" +
		"<p><b>Next step:</b> write subtract and its test</p>\n" +
		"<p><b>Why:</b> add is done, three functions remain</p>\n" +
		"<p>Continue by hand: <code>claude --resume " + demoSession + "</code></p>"
	if markup != wantHTML {
		t.Fatalf("unexpected html:\n%s", markup)
	}
}

func TestBackNoResumeNeeded(t *testing.T) {
	cases := []struct {
		action string
		folder string
		why    string
	}{
		{"none", "", "no session was cut off"},
		{"skip_done", demoFolder, "the task was already done"},
		{"skip_continued", demoFolder, "you already continued it"},
		{"skip_stuck", demoFolder, "the agent looked stuck"},
		{"skip_no_decision", demoFolder, "no Gemini decision"},
	}
	for _, testCase := range cases {
		subject, text, markup := Render(Event{
			Kind: KindBack, Provider: "claude", Action: testCase.action,
			SessionCwd: testCase.folder, Reason: "a raw reason is not used",
		})
		if subject != "Claude Code is back: no resume needed" {
			t.Fatalf("%s: unexpected subject: %s", testCase.action, subject)
		}
		first := "Claude Code is back. No resume was needed."
		if !strings.HasPrefix(text, first) {
			t.Fatalf("%s: unexpected text:\n%s", testCase.action, text)
		}
		if !strings.Contains(text, "Why: "+testCase.why) {
			t.Fatalf("%s: missing why %q:\n%s", testCase.action, testCase.why, text)
		}
		if strings.Contains(text, "Next step") || strings.Contains(text, "Continue by hand") {
			t.Fatalf("%s: no-resume email has resume fields:\n%s", testCase.action, text)
		}
		if testCase.folder == "" && strings.Contains(text, "Project:") {
			t.Fatalf("%s: empty project should be omitted:\n%s", testCase.action, text)
		}
		if !strings.Contains(markup, "<b>Claude Code is back.</b> No resume was needed.") {
			t.Fatalf("%s: unexpected html:\n%s", testCase.action, markup)
		}
	}
}

func TestNeedsYouEmail(t *testing.T) {
	subject, text, markup := Render(Event{Kind: KindNeedsYou, Provider: "claude", SessionCwd: demoFolder})
	if subject != "CarryOn needs you: Claude Code reset not confirmed" {
		t.Fatalf("unexpected subject: %s", subject)
	}
	if !strings.Contains(text, "CarryOn needs you. The Claude Code reset was not confirmed within 24 hours.") {
		t.Fatalf("unexpected text:\n%s", text)
	}
	if !strings.Contains(markup, "<b>CarryOn needs you.</b>") {
		t.Fatalf("unexpected html:\n%s", markup)
	}
}

func TestSimulatedEmailIsLabelled(t *testing.T) {
	subject, text, markup := Render(Event{Kind: KindBack, Provider: "claude", Action: "none", Simulated: true})
	if subject != "[SIMULATED] Claude Code is back: no resume needed" {
		t.Fatalf("unexpected subject: %s", subject)
	}
	if !strings.HasPrefix(text, "Simulated run: no real limit was hit.\n\n") {
		t.Fatalf("text not labelled:\n%s", text)
	}
	if !strings.HasPrefix(markup, "<p>Simulated run: no real limit was hit.</p>\n") {
		t.Fatalf("html not labelled:\n%s", markup)
	}
}

func TestCodexSubjectsAndCommands(t *testing.T) {
	subject, text, _ := Render(Event{
		Kind: KindBack, Provider: "codex", Action: "resume",
		SessionID: demoSession, SessionCwd: demoFolder, Prompt: "finish the parser",
	})
	if subject != "Codex is back: resumed carryon-demo-a2" {
		t.Fatalf("unexpected subject: %s", subject)
	}
	if !strings.Contains(text, "Continue by hand: codex exec resume "+demoSession) {
		t.Fatalf("wrong command:\n%s", text)
	}
}

func TestHTMLEscapesValues(t *testing.T) {
	_, text, markup := Render(Event{
		Kind: KindBack, Provider: "claude", Action: "resume",
		SessionID: demoSession, SessionCwd: `C:\work\a&b`,
		Prompt: `a < b & c > d`,
		Reason: `<script>alert("x")</script>`,
	})
	if strings.Contains(markup, "<script>") {
		t.Fatalf("raw script tag in html:\n%s", markup)
	}
	if !strings.Contains(markup, "&lt;script&gt;") {
		t.Fatalf("script not escaped:\n%s", markup)
	}
	if !strings.Contains(markup, "a &lt; b &amp; c &gt; d") {
		t.Fatalf("prompt not escaped:\n%s", markup)
	}
	if !strings.Contains(markup, "a&amp;b") {
		t.Fatalf("folder not escaped:\n%s", markup)
	}
	if !strings.Contains(text, `<script>`) {
		t.Fatal("plain text should keep the original characters")
	}
}

func TestHTMLUsesOnlyAllowedTags(t *testing.T) {
	_, _, markup := backResume()
	allowed := map[string]bool{"p": true, "b": true, "br": true, "code": true}
	tags := regexp.MustCompile(`</?([a-zA-Z0-9]+)`).FindAllStringSubmatch(markup, -1)
	for _, tag := range tags {
		if !allowed[strings.ToLower(tag[1])] {
			t.Fatalf("unexpected tag <%s> in:\n%s", tag[1], markup)
		}
	}
	for _, forbidden := range []string{"style=", "<table", "<img", "<font", "<html", "<body"} {
		if strings.Contains(strings.ToLower(markup), forbidden) {
			t.Fatalf("forbidden %q in html:\n%s", forbidden, markup)
		}
	}
}

func TestRedactsSecretsEverywhere(t *testing.T) {
	subject, text, markup := Render(Event{
		Kind: KindBack, Provider: "claude", Action: "resume",
		SessionCwd: demoFolder, Reason: "token sk-abcdefghijklmnopqrstuvwx",
		Prompt: "RESEND_API_KEY=re_zyxwvutsrqponmlkjihgfedc",
	})
	for _, secret := range []string{"sk-abcdefghijklmnopqrstuvwx", "re_zyxwvutsrqponmlkjihgfedc"} {
		for name, value := range map[string]string{"subject": subject, "text": text, "html": markup} {
			if strings.Contains(value, secret) {
				t.Fatalf("%s leaked %s", name, secret)
			}
		}
	}
	if !strings.Contains(text, "[REDACTED]") || !strings.Contains(markup, "[REDACTED]") {
		t.Fatal("missing redaction markers")
	}
}

func TestBackHasNoTimesOrTestOutput(t *testing.T) {
	_, text, markup := backResume()
	for _, unwanted := range []string{"Sent by", "UTC", "IST", "Test output", "Agent said", "Changed:"} {
		if strings.Contains(text, unwanted) || strings.Contains(markup, unwanted) {
			t.Fatalf("unexpected %q in back email", unwanted)
		}
	}
}
