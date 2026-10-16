//go:build windows

package main

// Windows coroutine worker transport is not implemented yet. Keep the
// cross-target closure ABI regression independent of console I/O, whose
// runtime path correctly requires that transport.
func main() {
	_ = sum(values)
}
