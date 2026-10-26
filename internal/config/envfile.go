package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
)

const maxEnvFileBytes = 64 * 1024

// ReadEnvFile parses a small KEY=VALUE file. It accepts comments, blank lines,
// CRLF, a UTF-8 BOM and single or double quotes. It never expands variables.
func ReadEnvFile(path string) (map[string]string, error) {
	values := map[string]string{}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return values, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read env file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("env file must be a regular file")
	}
	if info.Size() > maxEnvFileBytes {
		return nil, errors.New("env file is larger than 64 KiB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read env file: %w", err)
	}
	for number, line := range strings.Split(string(data), "\n") {
		if err := parseEnvLine(values, line); err != nil {
			return nil, fmt.Errorf("env file line %d: %w", number+1, err)
		}
	}
	return values, nil
}

func parseEnvLine(values map[string]string, raw string) error {
	line := strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}
	line = strings.TrimPrefix(line, "export ")
	name, value, found := strings.Cut(line, "=")
	name = strings.TrimSpace(name)
	if !found || !validEnvName(name) {
		return errors.New("expected NAME=value")
	}
	parsed, err := parseEnvValue(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	values[name] = parsed
	return nil
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for _, character := range name {
		if unicode.IsLetter(character) || unicode.IsDigit(character) || character == '_' {
			continue
		}
		return false
	}
	return true
}

func parseEnvValue(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	switch value[0] {
	case '"', '\'':
		return parseQuoted(value)
	}
	if index := strings.Index(value, " #"); index >= 0 {
		value = value[:index]
	}
	return strings.TrimSpace(value), nil
}

func parseQuoted(value string) (string, error) {
	quote := value[0]
	end := strings.IndexByte(value[1:], quote)
	if end < 0 {
		return "", errors.New("unterminated quoted value")
	}
	rest := strings.TrimSpace(value[end+2:])
	if rest != "" && !strings.HasPrefix(rest, "#") {
		return "", errors.New("unexpected text after quoted value")
	}
	return value[1 : end+1], nil
}
