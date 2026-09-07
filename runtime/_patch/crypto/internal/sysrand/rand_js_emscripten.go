//go:build llgo && js && wasm

//llgo:skip getRandomValues

package sysrand

import _ "unsafe"

// LLGo's js/wasm output uses the Emscripten host ABI rather than Go's gojs
// import module. Preserve the standard-library helper contract while resolving
// only this private host boundary to LLGo's C-ABI bridge.
//
//go:linkname getRandomValues runtime.getRandomData
func getRandomValues(p []byte)
