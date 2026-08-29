//go:build !darwin && !linux && !windows

package runtime

import (
	_ "sync/atomic"
	_ "unsafe"
)

// Targets without native parallel executors have one logical processor, so a
// procPin region needs no hosted exclusion primitive.
//
//go:linkname sync_runtime_procPin sync.runtime_procPin
func sync_runtime_procPin() int { return 0 }

//go:linkname sync_runtime_procUnpin sync.runtime_procUnpin
func sync_runtime_procUnpin() {}

//go:linkname atomic_runtime_procPin sync/atomic.runtime_procPin
func atomic_runtime_procPin() int { return 0 }

//go:linkname atomic_runtime_procUnpin sync/atomic.runtime_procUnpin
func atomic_runtime_procUnpin() {}
