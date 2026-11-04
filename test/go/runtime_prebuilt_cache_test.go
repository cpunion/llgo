package gotest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const runtimePrebuiltCacheProbe = `package main

import (
 "reflect"
 "runtime"
 "strings"
 "sync"
 "sync/atomic"
 "unsafe"
)

//go:linkname rowLoad runtime.prebuiltFuncCacheLoad
func rowLoad(int) unsafe.Pointer
//go:linkname rowStore runtime.prebuiltFuncCacheStore
func rowStore(int, unsafe.Pointer)

//go:linkname initRows runtime.initRuntimeFuncPCFrames
func initRows()
//go:linkname rowCount runtime.prebuiltFrameCount
func rowCount() int

//go:noinline
func mark0() int { return 0 }
//go:noinline
func mark1() int { return 1 }
//go:noinline
func mark2() int { return 2 }
//go:noinline
func mark3() int { return 3 }
//go:noinline
func mark4() int { return 4 }
//go:noinline
func mark5() int { return 5 }
//go:noinline
func mark6() int { return 6 }
//go:noinline
func mark7() int { return 7 }

var cachedFinalized uint32
var controlFinalized uint32
type gcControl struct { payload [128]byte }
type rowProbe struct {
 index int
 payload [4096]byte
}

func armGCControl() {
 value := new(gcControl)
 runtime.SetFinalizer(value, func(*gcControl) { atomic.AddUint32(&controlFinalized, 1) })
}

func primeRows() {
 var done sync.WaitGroup
 start := make(chan struct{})
 for index := range 8 {
  done.Add(1)
  go func(index int) {
   defer done.Done()
   <-start
   probe := &rowProbe{index: index}
   probe.payload[0] = byte(index)
   probe.payload[len(probe.payload)-1] = ^byte(index)
   runtime.SetFinalizer(probe, func(*rowProbe) { atomic.AddUint32(&cachedFinalized, 1) })
   rowStore(index, unsafe.Pointer(probe))
  }(index)
 }
 close(start)
 done.Wait()
}

func collectAndCheck() {
 // Store typed probes directly through the production row helpers. They
 // never enter either FuncForPC cache, which cannot mask missing row roots.
 // The host projection additionally checks cold zero allocation and uses
 // the precise collector; this native probe exercises the real GC roots.
 churn := make([][]byte, 128)
 for i := range churn { churn[i] = make([]byte, 16384) }
 runtime.GC()
 runtime.Gosched()
 runtime.KeepAlive(churn)
 if atomic.LoadUint32(&cachedFinalized) != 0 { panic("row cached probe finalized") }
 for index := range 8 {
  probe := (*rowProbe)(rowLoad(index))
  if probe == nil || probe.index != index || probe.payload[0] != byte(index) || probe.payload[len(probe.payload)-1] != ^byte(index) {
   panic("row cached probe lost after GC")
  }
 }
}

func checkSymbols(markers []func() int) {
 for _, marker := range markers {
  pc := reflect.ValueOf(marker).Pointer()
  fn := runtime.FuncForPC(pc)
  if fn == nil || fn.Entry() != pc || !strings.Contains(fn.Name(), "main.mark") {
   panic("row cached Func lost after GC")
  }
 }
}

func main() {
 // Production cache callers initialize the immutable frame table first.
 // Initialize that extent without a FuncForPC lookup, which would retain
 // objects in another cache and mask a missing row-cache root.
 before := rowCount()
 initRows()
 rows := rowCount()
 println("prebuilt row test extent", before, rows)
 if rows < 8 { panic("prebuilt row test has no initialized table extent") }
 for _, index := range []int{-1, 0, int(^uint(0)>>1)} {
  if rowLoad(index) != nil { panic("cold row read returned a Func") }
  rowStore(index, nil)
 }
 runtime.GOMAXPROCS(4)
 primeRows()
 armGCControl()
 for _, procs := range []int{1, 4} {
  runtime.GOMAXPROCS(procs)
  for range 12 { collectAndCheck() }
 }
 if atomic.LoadUint32(&controlFinalized) == 0 { panic("positive finalizer control did not run") }
 // Clear the test payloads before any FuncForPC lookup can interpret them.
 for index := range 8 {
  rowStore(index, nil)
  if rowLoad(index) != nil { panic("row clear failed") }
 }
 markers := []func() int{mark0, mark1, mark2, mark3, mark4, mark5, mark6, mark7}
 for _, procs := range []int{1, 4} {
  runtime.GOMAXPROCS(procs)
  for range 12 {
   checkSymbols(markers)
   runtime.GC()
  }
 }
 println("prebuilt row cache cold publication and GC ok")
}
`

func TestRuntimePrebuiltFuncCacheConcurrentFirstUse(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, []byte(runtimePrebuiltCacheProbe), 0644); err != nil {
		t.Fatal(err)
	}
	root := findRepoRoot(t)
	t.Setenv("LLGO_ROOT", root)
	cmd := commandForTest(t, root, "go", "run", "./cmd/llgo", "run", "-a", file)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prebuilt row-cache probe failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "prebuilt row cache cold publication and GC ok") {
		t.Fatalf("prebuilt row-cache probe did not finish: %s", output)
	}
	t.Logf("%s", output)
}
