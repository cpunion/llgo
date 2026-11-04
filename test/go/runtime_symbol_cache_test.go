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
 "sync/atomic"
 _ "unsafe"
)

//go:linkname recordFrameSymbol runtime.recordFrameSymbol
func recordFrameSymbol(pc, offset uintptr, name string)

var cachedFinalized uint32
var controlFinalized uint32

type cacheGCControl struct { padding [64]byte }

func armCacheGCControl() {
 p := new(cacheGCControl)
 runtime.SetFinalizer(p, func(*cacheGCControl) { atomic.AddUint32(&controlFinalized,1) })
}

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
   recordFrameSymbol(pc,8,"main.fallbackSymbolCache"+strconv.Itoa(i))
  }(i)
 }
 close(start)
 done.Wait()
 runtime.GC()
 prime := make(chan struct{})
 for i := 0; i < 64; i++ {
  done.Add(1)
  go func(i int) {
   defer done.Done()
   <-prime
   pc := uintptr(0x10000 + i*16)
   fn := runtime.FuncForPC(pc)
   want := "main.fallbackSymbolCache" + strconv.Itoa(i)
   if fn == nil || fn.Name() != want || fn.Entry() != pc-8 { panic("fallback symbol lost: "+want) }
   // FuncForPC's native fallback constructs heap Func objects. Distinct sets
   // keep all64 immutable objects cached without retaining external copies.
   runtime.SetFinalizer(fn,func(*runtime.Func) { atomic.AddUint32(&cachedFinalized,1) })
  }(i)
 }
 close(prime)
 done.Wait()
 armCacheGCControl()
 for _, procs := range []int{1,4} {
  runtime.GOMAXPROCS(procs)
  for repeat := 0; repeat < 12; repeat++ {
   churn := make([][]byte,128)
   for i := range churn { churn[i]=make([]byte,16384) }
   runtime.GC()
   runtime.Gosched()
   runtime.KeepAlive(churn)
   if atomic.LoadUint32(&cachedFinalized) != 0 { panic("cached Func finalized while retained") }
   for i := 0; i < 64; i++ {
    pc := uintptr(0x10000+i*16)
    want := "main.fallbackSymbolCache"+strconv.Itoa(i)
    fn := runtime.FuncForPC(pc)
    if fn == nil || fn.Name()!=want || fn.Entry()!=pc-8 { panic("cached Func lost after GC: "+want) }
   }
  }
 }
 if atomic.LoadUint32(&controlFinalized)==0 { panic("positive finalizer control did not run") }
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
