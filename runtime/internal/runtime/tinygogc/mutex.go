package tinygogc

import _ "unsafe"

// This is a single-mutator entry guard, NOT a cross-thread mutex. Neither
// concurrent mutators nor allocation from interrupt handlers is supported.
// Reject nested allocator/collector entry before it can modify the arena.
type mutex struct{ active bool }

func lock(m *mutex) {
	if m.active {
		gcReentryAbort()
		return
	}
	m.active = true
}

func unlock(m *mutex) { m.active = false }

// Do not format a Go panic or print through libc while the collector is busy:
// either can allocate and recurse into the failing entry guard again.
//
//go:linkname gcReentryAbort llvm.trap
func gcReentryAbort()
