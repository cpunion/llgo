//go:build wasip1 && wasm && llgo.wasi_threads

package runtime

const wasiThreadsTimerLLGoFiles = "; _wrap/timer_wait.c"
