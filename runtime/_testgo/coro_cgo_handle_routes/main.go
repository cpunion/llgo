package main

import (
	"os"
	"reflect"
	"runtime"
	"runtime/cgo"
	"sync"
)

type handleView interface{ Value() any }
type payload struct{ value int }

func construct(constructor func(any) cgo.Handle, value any) cgo.Handle {
	return constructor(value)
}

func exercise(value *payload) {
	handle := construct(cgo.NewHandle, value)
	defer handle.Delete()
	bound := handle.Value
	expression := cgo.Handle.Value
	var view handleView = handle
	runtime.Gosched()
	if handle.Value() != value || bound() != value || expression(handle) != value || view.Value() != value {
		panic("cgo handle changed through a managed call route")
	}
	reflected := reflect.ValueOf(handle).MethodByName("Value").Call(nil)
	if len(reflected) != 1 || reflected[0].Interface() != value {
		panic("cgo handle changed through a reflected bound method")
	}
	method, found := reflect.TypeOf(handle).MethodByName("Value")
	if !found {
		panic("cgo handle method metadata missing")
	}
	reflected = method.Func.Call([]reflect.Value{reflect.ValueOf(handle)})
	if len(reflected) != 1 || reflected[0].Interface() != value {
		panic("cgo handle changed through a reflected method expression")
	}
	if value.value%16 == 0 {
		runtime.GC()
		if bound() != value {
			panic("cgo handle did not retain its value across GC")
		}
	}
}

func main() {
	for _, parallelism := range []int{1, 2} {
		runtime.GOMAXPROCS(parallelism)
		var group sync.WaitGroup
		for worker := 0; worker < 8; worker++ {
			group.Go(func() {
				for index := 0; index < 16; index++ {
					exercise(&payload{value: worker*16 + index})
				}
			})
		}
		group.Wait()
	}
	const marker = "cgo handle managed routes passed\n"
	if count, err := os.Stdout.WriteString(marker); err != nil || count != len(marker) {
		panic("cgo handle marker not written")
	}
}
