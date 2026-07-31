//go:build !baremetal && (wasm || tinygo.wasm)

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package runtime

// WebAssembly coroutine frames are stackless and cannot be discovered by
// walking a native frame-pointer chain. Callers first consumes static/logical
// LLGo metadata and treats this physical fallback as unavailable.
func fpCallers(skip int, pc []uintptr) int {
	return 0
}

func fpUnwindAvailable() bool {
	return false
}

func callersWithPanicSplice(_ int, _ []uintptr) int {
	return 0
}
