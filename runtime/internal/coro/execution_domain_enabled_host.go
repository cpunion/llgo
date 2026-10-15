//go:build !llgo

package coro

// Host tests exercise the optional scheduler protocol without compiler folding.
func ExecutionDomainEnabled() bool { return true }

func GCObservationEnabled() bool { return true }
