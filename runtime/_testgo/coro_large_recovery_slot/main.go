package main

import "runtime"

type block [131073]byte

var recoveries, cleanups int

func named(fail bool) (out block) {
	defer func() {
		recovered := recover()
		if fail != (recovered != nil) {
			panic("unexpected recovery outcome")
		}
		if recovered != nil {
			recoveries++
			out[1] = 53
		}
		runtime.Gosched()
		runtime.GC()
		out[len(out)-1]++
		cleanups++
	}()
	var local block
	local[0], local[len(local)/2] = 37, 19
	runtime.Gosched()
	out = local
	if fail {
		panic("large named result")
	}
	return
}

func unnamed() block {
	var local block
	local[0] = 37
	defer func() {
		runtime.Gosched()
		local[0] = 99
		runtime.GC()
		cleanups++
	}()
	runtime.Gosched()
	return local // The return value is captured before the deferred mutation.
}

func main() {
	runtime.GOMAXPROCS(1)
	for i := 0; i < 3; i++ {
		normal := named(false)
		recovered := named(true)
		copied := unnamed()
		if normal[0] != 37 || normal[len(normal)/2] != 19 || normal[len(normal)-1] != 1 || normal[1] != 0 {
			panic("large normal named result lost cleanup writes")
		}
		if recovered[0] != 37 || recovered[len(recovered)/2] != 19 || recovered[len(recovered)-1] != 1 || recovered[1] != 53 {
			panic("large recovered named result lost cleanup writes")
		}
		if copied[0] != 37 {
			panic("large unnamed result observed a later defer mutation")
		}
	}
	if recoveries != 3 || cleanups != 9 {
		panic("large result cleanup count mismatch")
	}
	println("large recovery slots preserve named and unnamed results")
}
