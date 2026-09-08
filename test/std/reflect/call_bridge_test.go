package reflect_test

import (
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
