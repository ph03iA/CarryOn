// Package single owns the process-wide CarryOn lock.
package single

// Name is the per-session lock shared by watch and reset.
const Name = `Global\CarryOn`

// Acquire takes the lock, failing when another CarryOn holds it. On unix the
// lock lives in stateDir; Windows uses the global mutex name.
func Acquire(stateDir, name string) (*Lock, error) {
	if name == "" {
		name = Name
	}
	return acquire(stateDir, name)
}

// Release frees the lock.
func (l *Lock) Release() {
	if l == nil {
		return
	}
	release(l)
}
