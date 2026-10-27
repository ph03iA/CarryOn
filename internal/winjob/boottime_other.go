//go:build !windows && !linux && !darwin

package winjob

import (
	"errors"
	"time"
)

// bootTime is unknown here, so KillOrphanGroup never guesses.
func bootTime() (time.Time, error) {
	return time.Time{}, errors.New("boot time is not available on this platform")
}
