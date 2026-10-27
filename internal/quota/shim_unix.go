//go:build !windows

package quota

// isShim has no meaning off Windows; executables are native or not.
func isShim(string) bool { return false }
