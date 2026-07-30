//go:build tinygo.wasm

// The named WebAssembly targets replace block with the standard pure-Go body
// in block_generic_llgo.go. This exact source overlay masks the ARM-front-end
// assembly definition so the Go and assembly declarations cannot collide.
