//go:build !windows

package single

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Lock is a held flock on the state directory's lock file.
type Lock struct{ file *os.File }

func acquire(stateDir, _ string) (*Lock, error) {
	if stateDir == "" {
		return nil, errors.New("state directory is required")
	}
	file, err := os.OpenFile(filepath.Join(stateDir, "carryon.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open CarryOn lock: %w", err)
	}
	err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		file.Close()
		return nil, errors.New("another CarryOn is already running")
	}
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("lock CarryOn state: %w", err)
	}
	return &Lock{file: file}, nil
}

func release(lock *Lock) {
	if lock.file == nil {
		return
	}
	_ = unix.Flock(int(lock.file.Fd()), unix.LOCK_UN)
	lock.file.Close()
	lock.file = nil
}
