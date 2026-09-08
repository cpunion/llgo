package main

import (
	"os"
	"runtime"
	"runtime/debug"
)

var (
	initialPercent int
	retained       []byte
)

func init() {
	// Exercise the API before main, including normalization of negative old
	// values and restoration without rereading the environment.
	initialPercent = debug.SetGCPercent(-7)
	if old := debug.SetGCPercent(initialPercent); old != -1 {
		panic("negative GC percent was not normalized")
	}
}

func main() {
	want := 100
	switch os.Getenv("LLGO_GOGC_EXPECT") {
	case "off":
		want = -1
	case "0":
		want = 0
	case "1":
		want = 1
	}
	if initialPercent != want {
		println("initial GC percent", initialPercent, "want", want)
		panic("GOGC did not initialize SetGCPercent")
	}
	if err := os.Setenv("GOGC", "77"); err != nil {
		panic(err)
	}
	if old := debug.SetGCPercent(-1); old != want {
		panic("GOGC was reread after initialization")
	}
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	if stats.NextGC != ^uint64(0) {
		panic("disabled GC goal was not exposed")
	}
	if len(os.Args) > 1 && os.Args[1] == "startup" {
		println("wasm gc pacing startup ok")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "oom" {
		allocate(int(stats.HeapSys) + 64<<10)
		panic("disabled GC unexpectedly allocated beyond maximum memory")
	}
	testDisabledGrowth()
	testAutomatic(0)
	testAutomatic(1)
	debug.SetGCPercent(100)
	runtime.GC()
	runtime.ReadMemStats(&stats)
	if stats.NextGC <= stats.HeapAlloc || stats.NextGC < 4<<20 {
		panic("default next GC goal omitted the live main stack")
	}
	println("wasm gc pacing ok")
}

//go:noinline
func allocate(size int) {
	retained = make([]byte, size)
	retained[len(retained)-1] = 1
}

func testDisabledGrowth() {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	// More than the entire current heap cannot fit in the existing free space.
	// GOGC=off must grow instead of secretly collecting at capacity exhaustion.
	allocate(int(before.HeapSys) + 64<<10)
	runtime.ReadMemStats(&after)
	if after.NumGC != before.NumGC || after.HeapSys <= before.HeapSys {
		panic("disabled automatic GC did not preserve growth-only policy")
	}
	retained = nil
	runtime.GC()
	runtime.ReadMemStats(&after)
	if after.NumGC <= before.NumGC || after.NextGC != ^uint64(0) {
		panic("explicit GC did not run while automatic GC was disabled")
	}
	runtime.GC()
}

func testAutomatic(percent int) {
	debug.SetGCPercent(percent)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if before.NextGC <= before.HeapAlloc || before.NextGC == ^uint64(0) {
		panic("enabled GC goal is invalid")
	}
	// One allocation reaches the goal, independently of the initial heap size.
	allocate(int(before.NextGC-before.HeapAlloc) + 16)
	runtime.ReadMemStats(&after)
	if after.NumGC <= before.NumGC {
		panic("low GC percent did not trigger automatic collection")
	}
	retained = nil
	runtime.GC()
	runtime.ReadMemStats(&after)
	lowGoal := after.NextGC
	debug.SetGCPercent(100)
	runtime.ReadMemStats(&after)
	if lowGoal >= after.NextGC {
		panic("low GC percent was silently clamped to 100")
	}
}
