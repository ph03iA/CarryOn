//go:build !windows

package single

import "testing"

func TestLockIsExclusive(t *testing.T) {
	dir := t.TempDir()
	lock, err := Acquire(dir, Name)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer lock.Release()
	_, err = Acquire(dir, Name)
	if err == nil {
		t.Fatal("a second CarryOn must not acquire the same lock")
	}
	if err.Error() != "another CarryOn is already running" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLockCanBeReacquiredAfterRelease(t *testing.T) {
	dir := t.TempDir()
	lock, err := Acquire(dir, Name)
	if err != nil {
		t.Fatal(err)
	}
	lock.Release()
	again, err := Acquire(dir, Name)
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	again.Release()
}

func TestLockIsPerStateDir(t *testing.T) {
	first, err := Acquire(t.TempDir(), Name)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	second, err := Acquire(t.TempDir(), Name)
	if err != nil {
		t.Fatalf("another state directory must not conflict: %v", err)
	}
	second.Release()
}
