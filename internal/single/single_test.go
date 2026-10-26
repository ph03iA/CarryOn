package single

import "testing"

func TestMutexIsExclusive(t *testing.T) {
	lock, err := Acquire(Name + `Test`)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer lock.Release()
	if _, err := Acquire(Name + `Test`); err == nil {
		t.Fatal("a second CarryOn must not acquire the same mutex")
	}
}

func TestMutexCanBeReacquiredAfterRelease(t *testing.T) {
	lock, err := Acquire(Name + `Test2`)
	if err != nil {
		t.Fatal(err)
	}
	lock.Release()
	again, err := Acquire(Name + `Test2`)
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	again.Release()
}
