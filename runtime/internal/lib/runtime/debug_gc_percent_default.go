//go:build !wasm || !llgo.wasm.gc.linear

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package runtime

import _ "unsafe"

var llgoGCPercent int32 = 100

//go:linkname setGCPercent runtime/debug.setGCPercent
func setGCPercent(in int32) (out int32) {
	out = llgoGCPercent
	llgoGCPercent = in
	return out
}
