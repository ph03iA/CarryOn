//go:build windows

package single

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

func acquire(name string) (*Lock, error) {
	handle, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr(name))
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("another CarryOn is already running")
	}
	if err != nil {
		return nil, fmt.Errorf("create CarryOn mutex: %w", err)
	}
	return &Lock{handle: uintptr(handle)}, nil
}

func release(lock *Lock) {
	if lock.handle != 0 {
		windows.CloseHandle(windows.Handle(lock.handle))
		lock.handle = 0
	}
}