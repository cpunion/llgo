//go:build llgo && wasm && wasip1 && llgo.wasi_threads

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package runtime

// The pthreads backend does not use single-worker heap attribution. Keep its
// existing caller instrumentation independent of the single-worker raw G.
func memProfileHasCurrentG() bool { return true }
