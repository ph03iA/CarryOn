package redact

import (
	"strings"
	"testing"
)

func TestTextRemovesCredentialShapes(t *testing.T) {
	cases := map[string]string{
		"anthropic": "sk-ant-abcdefghijklmnopqrstuvwxyz",
		"google":    "AIzaSyD-abcdefghijklmnopqrstuvwxyz12345",
		"github":    "ghp_abcdefghijklmnopqrstuvwxyz0123",
		"pat":       "github_pat_11ABCDEFG0123456789_abcdefghij",
		"aws":       "AKIAIOSFODNN7EXAMPLE",
		"slack":     "xoxb-123456789012-abcdefghijkl",
		"bearer":    "Authorization: Bearer abcdefghijklmnopqrstuvwxyz",
		"assigned":  "RESEND_API_KEY=re_abcdefghijklmnop",
	}
	for name, secret := range cases {
		result := Text("before " + secret + " after")
		if strings.Contains(result.Text, secret) {
			t.Fatalf("%s secret survived: %s", name, result.Text)
		}
		if result.Removed == 0 {
			t.Fatalf("%s: no redaction counted", name)
		}
		if !strings.Contains(result.Text, "before") || !strings.Contains(result.Text, "after") {
			t.Fatalf("%s: redaction ate surrounding text: %s", name, result.Text)
		}
	}
}

func TestTextRemovesPrivateKeys(t *testing.T) {
	input := "-----BEGIN RSA PRIVATE KEY-----\nMIIEow\n-----END RSA PRIVATE KEY-----"
	if !ContainsSecret(input) {
		t.Fatal("private key not detected")
	}
	result := Text(input)
	if ContainsSecret(result.Text) {
		t.Fatalf("private key survived: %s", result.Text)
	}
}

func TestContainsSecretIsFalseForOrdinaryText(t *testing.T) {
	if ContainsSecret("fix the failing checkout test") {
		t.Fatal("ordinary text flagged as a secret")
	}
}

func TestTextRedactsSingleQuotedValue(t *testing.T) {
	result := Text(`API_KEY='supersecretvalue'`)
	if strings.Contains(result.Text, "supersecretvalue") {
		t.Fatalf("single-quoted secret survived: %s", result.Text)
	}
	if !strings.Contains(result.Text, "[REDACTED]") {
		t.Fatalf("missing redaction marker: %s", result.Text)
	}
}
