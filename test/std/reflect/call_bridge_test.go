//go:build wasm

package reflect_test

import (
	"io"
	"reflect"
	"runtime"
	"testing"
)

type bridgeRecord struct {
	Label string
	Value int64
	Next  *int
}

func (v *bridgeRecord) Read() int64 {
	if v == nil {
		return -1
	}
	return v.Value
}

type bridgeCounter int

func (v bridgeCounter) Add(n int) int { return int(v) + n }

type bridgeGCMethod struct{}

func (*bridgeGCMethod) Pointer() *byte {
	runtime.GC()
	runtime.Gosched()
	return nil
}

func (*bridgeGCMethod) Error(err error) error {
	runtime.GC()
	runtime.Gosched()
	return err
}

func TestReflectMethodValueGCBridge(t *testing.T) {
	// A method extracted as a typed function crosses the MakeFunc adapter as
	// well as the reflected call adapter. Both must survive a suspension.
	v := reflect.ValueOf(&bridgeGCMethod{})
	pointer := v.MethodByName("Pointer").Interface().(func() *byte)
	errorValue := v.MethodByName("Error").Interface().(func(error) error)
	for i := 0; i < 3; i++ {
		if got := pointer(); got != nil {
			t.Fatalf("pointer method returned %p, want nil", got)
		}
		for _, input := range []error{nil, io.EOF} {
			if got := errorValue(input); got != input {
				t.Fatalf("error method returned %v, want %v", got, input)
			}
		}
	}
}

func TestReflectMethodExpressionBridge(t *testing.T) {
	p := &bridgeRecord{Value: 23}
	m, ok := reflect.TypeOf(p).MethodByName("Read")
	if !ok || m.Func.Call([]reflect.Value{reflect.ValueOf(p)})[0].Int() != 23 {
		t.Fatal("pointer receiver method expression call")
	}
	v := bridgeCounter(4)
	m, ok = reflect.TypeOf(v).MethodByName("Add")
	if !ok || m.Func.Call([]reflect.Value{reflect.ValueOf(v), reflect.ValueOf(5)})[0].Int() != 9 {
		t.Fatal("value receiver method expression call")
	}
}

func TestReflectTypedCallBridge(t *testing.T) {
	x := 42
	captured := &bridgeRecord{Label: "kept", Value: 17, Next: &x}
	fn := func(v bridgeRecord, empty struct{}, f func(int) int) (bridgeRecord, float64, func() int) {
		runtime.GC()
		runtime.Gosched()
		v.Value += captured.Value
		v.Value += int64(f(*v.Next))
		return v, 1.25, func() int { return *captured.Next }
	}
	got := reflect.ValueOf(fn).Call([]reflect.Value{
		reflect.ValueOf(*captured), reflect.ValueOf(struct{}{}),
		reflect.ValueOf(func(n int) int { return n + 1 }),
	})
	if v := got[0].Interface().(bridgeRecord); v.Label != "kept" || v.Value != 77 || *v.Next != 42 {
		t.Fatalf("aggregate result = %+v", v)
	}
	if got[1].Float() != 1.25 || got[2].Interface().(func() int)() != 42 {
		t.Fatal("scalar or closure result was corrupted")
	}
	for _, receiver := range []*bridgeRecord{nil, captured} {
		want := int64(-1)
		if receiver != nil {
			want = 17
		}
		var iface interface{ Read() int64 } = receiver
		for _, method := range []reflect.Value{
			reflect.ValueOf(receiver).MethodByName("Read"),
			reflect.ValueOf(&iface).Elem().MethodByName("Read"),
		} {
			if got := method.Call(nil)[0].Int(); got != want {
				t.Fatalf("method result = %d, want %d", got, want)
			}
		}
	}
}

func TestReflectTypedMakeFuncBridge(t *testing.T) {
	type signature func(bridgeRecord, struct{}, func(int) int) (bridgeRecord, float64, func() int)
	var saved []reflect.Value
	value := reflect.MakeFunc(reflect.TypeOf(signature(nil)), func(in []reflect.Value) []reflect.Value {
		saved = in
		runtime.GC()
		runtime.Gosched()
		v := in[0].Interface().(bridgeRecord)
		v.Value += int64(in[2].Interface().(func(int) int)(*v.Next))
		return []reflect.Value{reflect.ValueOf(v), reflect.ValueOf(2.5), reflect.ValueOf(func() int { return *v.Next })}
	})
	f := value.Interface().(signature)
	x := 42
	arg := bridgeRecord{Label: "copy", Value: 7, Next: &x}
	v, n, next := f(arg, struct{}{}, func(n int) int { return n + 1 })
	if v.Value != 50 || n != 2.5 || next() != 42 {
		t.Fatalf("MakeFunc results = (%+v, %g, %d)", v, n, next())
	}
	arg.Label = "changed"
	runtime.GC()
	if got := saved[0].Interface().(bridgeRecord); got.Label != "copy" || *got.Next != 42 {
		t.Fatalf("escaped argument = %+v, want an independent copy", got)
	}
	result := make(chan int64, 1)
	go func() {
		v, _, _ := f(arg, struct{}{}, func(n int) int { return n })
		result <- v.Value
	}()
	if got := <-result; got != 49 {
		t.Fatalf("goroutine MakeFunc result = %d", got)
	}
}

func TestReflectDynamicMakeFuncBridge(t *testing.T) {
	// This exact signature does not appear at any compiled call site.
	record := reflect.StructOf([]reflect.StructField{{Name: "Dynamic", Type: reflect.TypeOf(0)}})
	typ := reflect.FuncOf([]reflect.Type{record}, []reflect.Type{record}, false)
	f := reflect.MakeFunc(typ, func(in []reflect.Value) []reflect.Value {
		runtime.GC()
		return in
	})
	arg := reflect.New(record).Elem()
	arg.Field(0).SetInt(91)
	got := f.Call([]reflect.Value{arg})
	if len(got) != 1 || got[0].Field(0).Int() != 91 {
		t.Fatalf("dynamic MakeFunc result = %v", got)
	}
}

// A MakeFunc entry and an extracted method value are transparent wrappers
// for a directly deferred recover, but must not make an indirect call direct.
func TestReflectDeferredRecoverBridge(t *testing.T) {
	const marker = "reflection deferred panic"
	var recovered any
	f := reflect.MakeFunc(reflect.TypeOf((func())(nil)), func([]reflect.Value) []reflect.Value {
		recovered = recover()
		runtime.GC()
		return nil
	}).Interface().(func())
	if escaped := bridgePanicWithDeferred(f, marker); escaped != nil || recovered != marker {
		t.Fatalf("direct MakeFunc recover = %v, escaped panic = %v", recovered, escaped)
	}
	recovered = nil
	if escaped := bridgePanicWithDeferred(func() { f() }, marker); escaped != marker || recovered != nil {
		t.Fatalf("indirect MakeFunc recover = %v, escaped panic = %v", recovered, escaped)
	}
	for _, indirect := range []bool{false, true} {
		recovered = nil
		receiver := &bridgeRecoverer{recovered: &recovered}
		method := reflect.ValueOf(receiver).MethodByName("Recover").Interface().(func())
		deferred := method
		if indirect {
			deferred = func() { method() }
		}
		escaped := bridgePanicWithDeferred(deferred, marker)
		if indirect {
			if escaped != marker || recovered != nil {
				t.Fatalf("indirect method recover = %v, escaped panic = %v", recovered, escaped)
			}
		} else if escaped != nil || recovered != marker {
			t.Fatalf("direct method recover = %v, escaped panic = %v", recovered, escaped)
		}
	}
}

//go:noinline
func bridgePanicWithDeferred(f func(), marker any) (escaped any) {
	defer func() { escaped = recover() }()
	defer f()
	panic(marker)
}

type bridgeRecoverer struct {
	recovered *any
}

//go:noinline
func (r *bridgeRecoverer) Recover() {
	*r.recovered = recover()
	runtime.GC()
}

func TestReflectVariadicMakeFuncBridge(t *testing.T) {
	type signature func(int, ...bridgeRecord) (int64, []bridgeRecord)
	value := reflect.MakeFunc(reflect.TypeOf(signature(nil)), func(in []reflect.Value) []reflect.Value {
		if len(in) != 2 || in[1].Kind() != reflect.Slice {
			t.Fatalf("variadic callback arguments = %v", in)
		}
		runtime.GC()
		items := in[1].Interface().([]bridgeRecord)
		total := in[0].Int()
		for _, item := range items {
			total += item.Value
		}
		return []reflect.Value{reflect.ValueOf(total), in[1]}
	})
	f := value.Interface().(signature)
	if n, items := f(3); n != 3 || items != nil {
		t.Fatalf("empty variadic result = (%d, %v)", n, items)
	}
	items := []bridgeRecord{{Label: "first", Value: 7}, {Label: "second", Value: 11}}
	if n, got := f(3, items...); n != 21 || len(got) != 2 || &got[0] != &items[0] {
		t.Fatalf("typed variadic result = (%d, %v)", n, got)
	}
	called := value.Call([]reflect.Value{reflect.ValueOf(3), reflect.ValueOf(items[0]), reflect.ValueOf(items[1])})
	sliced := value.CallSlice([]reflect.Value{reflect.ValueOf(3), reflect.ValueOf(items)})
	for _, got := range [][]reflect.Value{called, sliced} {
		if got[0].Int() != 21 || !reflect.DeepEqual(got[1].Interface(), items) {
			t.Fatalf("reflected variadic result = %v", got)
		}
	}
}

type bridgeReader interface {
	Read() int64
}

func TestReflectInterfaceMakeFuncBridge(t *testing.T) {
	type signature func(bridgeReader) bridgeReader
	value := reflect.MakeFunc(reflect.TypeOf(signature(nil)), func(in []reflect.Value) []reflect.Value {
		runtime.GC()
		runtime.Gosched()
		return in
	})
	f := value.Interface().(signature)
	plain := reflect.ValueOf(func(v bridgeReader) bridgeReader {
		runtime.GC()
		return v
	})
	var nilPointer *bridgeRecord
	for _, input := range []bridgeReader{nil, nilPointer, &bridgeRecord{Value: 37}} {
		if got := f(input); got != input {
			t.Fatalf("typed interface roundtrip = %v, want %v", got, input)
		}
		for _, callable := range []reflect.Value{value, plain} {
			out := callable.Call([]reflect.Value{reflect.ValueOf(&input).Elem()})[0]
			if got := out.Interface(); got != input {
				t.Fatalf("reflected interface roundtrip = %v, want %v", got, input)
			}
			if !out.IsNil() && out.Interface().(bridgeReader).Read() != input.Read() {
				t.Fatal("interface result method table was corrupted")
			}
		}
	}
	concrete := &bridgeRecord{Value: 43}
	assigned := reflect.MakeFunc(reflect.TypeOf((func() bridgeReader)(nil)), func([]reflect.Value) []reflect.Value {
		return []reflect.Value{reflect.ValueOf(concrete)}
	}).Interface().(func() bridgeReader)()
	if assigned.Read() != 43 {
		t.Fatal("concrete result assignment lost the interface method table")
	}
}
