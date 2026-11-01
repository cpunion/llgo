//go:build llgo && coro_context_layout_test

// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package main

import (
	"fmt"
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/runtime"
)

func main() {
	layout, failure := runtime.CoroContextLayoutForTesting()
	if failure != "" {
		panic(failure)
	}
	if unsafe.Sizeof(uintptr(0)) == 8 {
		want := [9]uintptr{248, 96, 104, 352, 0, 0, 248, 48, 56}
		if layout != want {
			panic(fmt.Sprintf("native task layout = %v, want %v", layout, want))
		}
	}
	fmt.Println("context layout and ownership ok", layout)
}
