//go:build llgo && (darwin || linux) && !baremetal && nogc

package corofleet

// Nogc builds own an ordinary joinable libc pthread.
const (
	LLGoFiles   = "_owner/owner.c"
	LLGoPackage = "link"
)
