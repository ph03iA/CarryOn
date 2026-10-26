// Package redact removes credential-shaped text before it leaves the machine.
package redact

import (
	"regexp"
)

const replacement = "[REDACTED]"

// Result reports the scrubbed text and how many spans were replaced.
type Result struct {
	Text    string
	Removed int
}

var patterns = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`AIza[0-9A-Za-z_-]{30,}`),
	regexp.MustCompile(`ghp_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`gho_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/-]{16,}=*`),
	regexp.MustCompile(`re_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:API_?KEY|TOKEN|SECRET|PASSWORD|PASSWD)["']?\s*[=:]\s*)(["']?)[^\s"']{8,}["']?`),
}

// Text replaces every credential-shaped span and reports the replacement count.
func Text(input string) Result {
	output := input
	removed := 0
	for _, pattern := range patterns {
		matches := pattern.FindAllStringIndex(output, -1)
		if len(matches) == 0 {
			continue
		}
		removed += len(matches)
		output = pattern.ReplaceAllString(output, replacement)
	}
	return Result{Text: output, Removed: removed}
}

// ContainsSecret reports whether any pattern matches. Used by tests to prove
// that a payload never carried a secret to an external service.
func ContainsSecret(input string) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(input) {
			return true
		}
	}
	return false
}
