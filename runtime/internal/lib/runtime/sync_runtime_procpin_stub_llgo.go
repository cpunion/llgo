//go:build (!darwin && !linux && !windows) || baremetal || tinygo.wasm

package runtime

import (
	_ "unsafe"
)

// Targets without native parallel executors have one logical processor, so a
// procPin region needs no hosted exclusion primitive.
//
//go:linkname sync_runtime_procPin sync.runtime_procPin
func sync_runtime_procPin() int { return 0 }

//go:linkname sync_runtime_procUnpin sync.runtime_procUnpin
func sync_runtime_procUnpin() {}
