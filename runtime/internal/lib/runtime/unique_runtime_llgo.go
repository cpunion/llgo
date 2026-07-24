//go:build darwin || linux

package runtime

import (
	_ "unsafe"
)

var uniqueMapCleanup = make(chan struct{}, 1)

//llgo:managedlink
//go:linkname unique_runtime_registerUniqueMapCleanup unique.runtime_registerUniqueMapCleanup
func unique_runtime_registerUniqueMapCleanup(f func()) {
	// Start the goroutine in the runtime so it's counted as a system goroutine.
	go func(cleanup func()) {
		for {
			<-uniqueMapCleanup
			cleanup()
		}
	}(f)
}

func unique_runtime_notifyMapCleanup() {
	if uniqueMapCleanup == nil {
		return
	}
	select {
	case uniqueMapCleanup <- struct{}{}:
	default:
	}
}
