//go:build llgo && (darwin || linux) && !baremetal && !nogc

package coroworker

// Collector builds must create workers through BDWGC's pthread wrapper. This
// registers each native stack before the POD completion callback can enter Go
// and pairs with pthread.Join's GC_pthread_join binding.
const (
	LLGoFiles   = "$(pkg-config --cflags bdw-gc) -DGC_THREADS=1 -DLLGO_CORO_WORKER_BDWGC=1: _worker/worker.c"
	LLGoPackage = "link: $(pkg-config --libs bdw-gc); -lgc"
)
