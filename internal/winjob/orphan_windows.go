//go:build windows

package winjob

import "time"

// KillOrphanGroup does nothing on Windows: the kill-on-close job already
// stopped the agent when CarryOn died.
func KillOrphanGroup(int, time.Time) bool { return false }
