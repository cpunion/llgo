//go:build llgo && (darwin || linux) && !baremetal && !wasm && !tinygo.wasm

package coroalloc

// LLGoFiles adds the bounded native size-class cache. Its C bin metadata keeps
// the lazily computed class capacity in otherwise unused alignment padding.
const LLGoFiles = "_cache/cache.c"
