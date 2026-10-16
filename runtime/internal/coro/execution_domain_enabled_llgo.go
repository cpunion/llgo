//go:build llgo

package coro

import _ "unsafe"

// ExecutionDomainEnabled is replaced by the compiler's immutable, whole-program
// capability. Ordinary programs must not retain domain probes or side tables.
// The declaration has no callable fallback: an unsupported compiler must fail
// instead of silently disabling domain isolation.
//
//go:linkname ExecutionDomainEnabled llgo.coroExecutionDomainEnabled
func ExecutionDomainEnabled() bool

// GCObservationEnabled is frozen from reachable observer registration. Its
// false value removes the observer call and collector hook before emission;
// a program without channel timers need not link the standard timer runtime.
//
//go:linkname GCObservationEnabled llgo.coroGCObservationEnabled
func GCObservationEnabled() bool
