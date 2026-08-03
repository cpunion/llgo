package main

import (
	"bytes"
	"encoding/base64"
	"math"
	"reflect"
	"regexp"
	"runtime"
	"strings"
)

type payload struct {
	Value int
}

type reflectedValue struct {
	base int
}

func (value reflectedValue) Add(delta int) int {
	runtime.Gosched()
	return value.base + delta
}

//go:noinline
func variadicSuspend(ready chan<- struct{}, resume <-chan struct{}, base int, values ...int) int {
	live := &payload{Value: base}
	ready <- struct{}{}
	<-resume
	runtime.GC()
	result := live.Value
	for _, value := range values {
		result += value
	}
	return result
}

func testVariadicIndirectCall() {
	call := variadicSuspend
	ready := make(chan struct{})
	resume := make(chan struct{})
	done := make(chan int)
	go func() {
		done <- call(ready, resume, 10, 1, 2, 3)
	}()
	<-ready
	runtime.GC()
	close(resume)
	if got := <-done; got != 16 {
		panic("variadic indirect call lost state")
	}
}

func testReflectionCalls() {
	value := reflect.ValueOf(reflectedValue{base: 40})
	method := value.MethodByName("Add")
	add, ok := method.Interface().(func(int) int)
	if !ok || add(2) != 42 {
		panic("reflected method returned the wrong value")
	}

	typ := value.Type()
	field, ok := typ.FieldByName("base")
	methodType, hasMethod := typ.MethodByName("Add")
	if !ok || field.Type.Kind() != reflect.Int || !hasMethod || methodType.Type.NumIn() != 2 {
		panic("reflection metadata is incomplete")
	}
	methodExpr, ok := methodType.Func.Interface().(func(reflectedValue, int) int)
	if !ok || methodExpr(reflectedValue{base: 41}, 1) != 42 {
		panic("reflected method expression returned the wrong value")
	}

	items := reflect.MakeMapWithSize(reflect.TypeOf(map[string]int{}), 1)
	items.SetMapIndex(reflect.ValueOf("answer"), reflect.ValueOf(42))
	if got := items.MapIndex(reflect.ValueOf("answer")); !got.IsValid() || got.Int() != 42 {
		panic("reflection map workload failed")
	}
}

//export wasm_acceptance_export
func wasm_acceptance_export(value int32) int32 {
	live := &payload{Value: int(value)}
	runtime.GC()
	return int32(live.Value + 1)
}

func testCExportRootEntry() {
	if got := callWasmAcceptanceExport(41); got != 42 {
		panic("C export root entry returned the wrong value")
	}
}

//go:noinline
func pclnProbe() bool {
	runtime.Gosched()
	var pcs [32]uintptr
	frames := runtime.CallersFrames(pcs[:runtime.Callers(0, pcs[:])])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, ".pclnProbe") && frame.File != "" && frame.Line > 0 {
			return true
		}
		if !more {
			return false
		}
	}
}

func testPCLNAfterResume() {
	if !pclnProbe() {
		panic("pclntab did not report the resumed Go frame")
	}
}

func testStandardLibraryWorkloads() {
	input := bytes.Repeat([]byte("llgo-wasm-"), 32)
	encoded := base64.StdEncoding.EncodeToString(input)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || !bytes.Equal(decoded, input) {
		panic("encoding/base64 round trip failed")
	}
	matched, err := regexp.MatchString(`^llgo-(wasm|native)-[0-9]+$`, "llgo-wasm-42")
	if err != nil || !matched {
		panic("regexp workload failed")
	}
	if math.Floor(1.75) != 1 || math.Ceil(-1.75) != -1 || math.Trunc(-1.75) != -1 {
		panic("math rounding workload failed")
	}
}

func main() {
	testVariadicIndirectCall()
	testReflectionCalls()
	testCExportRootEntry()
	testPCLNAfterResume()
	testStandardLibraryWorkloads()
	println("wasm resume acceptance ok")
}
