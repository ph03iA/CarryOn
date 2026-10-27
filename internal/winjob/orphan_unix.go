//go:build !windows

package winjob

import "time"

// KillOrphanGroup kills a process group recorded by a CarryOn that died while
// the agent was running. It acts only when the machine has not rebooted since
// startedAt, because after a reboot the id may belong to an unrelated group.
func KillOrphanGroup(pgid int, startedAt time.Time) bool {
	booted, err := bootTime()
	if err != nil || pgid <= 0 || !booted.Before(startedAt) {
		return false
	}
	killGroup(pgid)
	return true
}
