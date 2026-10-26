// Package single owns the process-wide CarryOn mutex.
package single

// Name is the per-session mutex shared by watch and simulate.
const Name = `Local\CarryOn`

// Lock is a held mutex.
type Lock struct{ handle uintptr }

// Acquire takes the named mutex, failing when another CarryOn holds it.
func Acquire(name string) (*Lock, error) {
	if name == "" {
		name = Name
	}
	return acquire(name)
}

// Release frees the mutex.
func (l *Lock) Release() {
	if l == nil {
		return
	}
	release(l)
}
