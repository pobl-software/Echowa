//go:build !linux

package setup

// flushInput does nothing here: setup only runs on Linux.
func flushInput(int) {}
