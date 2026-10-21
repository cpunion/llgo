// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package gotest

import (
	"os"
	"path/filepath"
	"testing"
)

// Exercise the native fallback separately from the prebuilt Go frame table.
// Concurrent first writers must publish one shared cache, and an intervening
// collection must retain the dynamically constructed symbol names.
const runtimeFallbackSymbolCacheProbe = `package main

import (
	"runtime"
	"strconv"
	"sync"
	_ "unsafe"
)

//go:linkname recordFrameSymbol runtime.recordFrameSymbol
func recordFrameSymbol(pc, offset uintptr, name string)

func main() {
	runtime.GOMAXPROCS(4)
	start := make(chan struct{})
	var done sync.WaitGroup
	for i := 0; i < 64; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			<-start
			pc := uintptr(0x10000 + i*16)
			recordFrameSymbol(pc, 8, "main.fallbackSymbolCache"+strconv.Itoa(i))
		}(i)
	}
	close(start)
	done.Wait()
	runtime.GC()
	for i := 0; i < 64; i++ {
		pc := uintptr(0x10000 + i*16)
		want := "main.fallbackSymbolCache" + strconv.Itoa(i)
		for repeat := 0; repeat < 2; repeat++ {
			fn := runtime.FuncForPC(pc)
			if fn == nil || fn.Name() != want || fn.Entry() != pc-8 {
				panic("fallback symbol lost: " + want)
			}
		}
	}
}
`

func TestRuntimeFallbackSymbolCacheConcurrentFirstUse(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, []byte(runtimeFallbackSymbolCacheProbe), 0644); err != nil {
		t.Fatal(err)
	}
	root := findRepoRoot(t)
	t.Setenv("LLGO_ROOT", root)
	cmd := commandForTest(t, root, "go", "run", "./cmd/llgo", "run", "-a", file)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("llgo fallback symbol cache probe failed: %v\n%s", err, out)
	}
}
