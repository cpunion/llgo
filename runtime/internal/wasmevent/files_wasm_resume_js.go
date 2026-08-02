//go:build llgo && js && wasm && llgo.wasm_resume && !llgo.wasm_workers

package wasmevent

const LLGoFiles = "_wrap/event_wasm.c; _wrap/resume_wasm.c"
