//go:build linux

package winjob

import (
	"time"

	"golang.org/x/sys/unix"
)

func bootTime() (time.Time, error) {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return time.Time{}, err
	}
	return time.Now().Add(-time.Duration(info.Uptime) * time.Second), nil
}
