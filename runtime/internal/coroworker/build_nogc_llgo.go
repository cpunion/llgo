//go:build llgo && (darwin || linux) && !baremetal && nogc

package coroworker

// Nogc builds own ordinary libc pthreads and pair them with pthread_join.
const (
	LLGoFiles   = "_worker/worker.c"
	LLGoPackage = "link"
)
