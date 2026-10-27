//go:build windows

package quota

import (
	"path/filepath"
	"strings"
)

func isShim(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cmd", ".bat", ".ps1":
		return true
	}
	return false
}
