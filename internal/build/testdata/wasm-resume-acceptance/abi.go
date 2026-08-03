package main

import _ "unsafe"

const LLGoFiles = "abi.c"

//go:linkname callWasmAcceptanceExport C.llgo_call_wasm_acceptance_export
func callWasmAcceptanceExport(int32) int32
