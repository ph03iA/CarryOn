//go:build !windows

package single

// acquire is a no-op off Windows, where CarryOn does not ship.
func acquire(string) (*Lock, error) { return &Lock{}, nil }

func release(*Lock) {}