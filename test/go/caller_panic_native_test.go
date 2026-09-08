//go:build !wasm

package gotest

import "testing"

// Wasm cannot create this subprocess from the guest. Its mandatory host check
// launches the same init-time panic using the retained test binary instead.
func TestCallerPanicTraceback(t *testing.T) {
	testCallerPanicTraceback(t)
}
