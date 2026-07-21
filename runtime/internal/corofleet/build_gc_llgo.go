//go:build llgo && (darwin || linux) && !baremetal && !nogc

package corofleet

// Collector builds create the scheduler peer through BDWGC's pthread wrapper
// so its fixed native stack is registered before the raw runtime owner enters
// Go. pthread.Join uses the matching GC_pthread_join binding.
const (
	LLGoFiles   = "$(pkg-config --cflags bdw-gc) -DGC_THREADS=1 -DLLGO_CORO_FLEET_BDWGC=1: _owner/owner.c"
	LLGoPackage = "link: $(pkg-config --libs bdw-gc); -lgc"
)
