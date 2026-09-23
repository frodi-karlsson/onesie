//go:build !windows

package engine

func platformBrokenPipe(_ error) bool {
	return false
}
