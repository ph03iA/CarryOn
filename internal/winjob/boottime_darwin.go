//go:build darwin

package winjob

import (
	"time"

	"golang.org/x/sys/unix"
)

func bootTime() (time.Time, error) {
	value, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(value.Unix()), nil
}
