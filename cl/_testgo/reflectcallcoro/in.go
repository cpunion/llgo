package main

import (
	"reflect"
	"time"
)

type empty struct{}

type alignedZero [0]int64

type record struct {
	Small int16
	Large int64
}

type counter struct {
	value int
}

func (c *counter) Add(delta int) (empty, record) {
	time.Sleep(time.Millisecond)
	c.value += delta
	return empty{}, record{Small: int16(delta), Large: int64(c.value)}
}

type adder interface {
	Add(int) (empty, record)
}

func named(value int) int {
	time.Sleep(time.Millisecond)
	return value + 1
}

func variadic(prefix string, values ...int) (empty, record, string) {
	var sum int64
	for _, value := range values {
		sum += int64(value)
	}
	time.Sleep(time.Millisecond)
	return empty{}, record{Small: int16(len(values)), Large: sum}, prefix + "!"
}

func apply(fn func(int) int, value int) int {
	time.Sleep(time.Millisecond)
	return fn(value)
}

func alignedResults() (byte, alignedZero, byte) {
	time.Sleep(time.Millisecond)
	return 1, alignedZero{}, 2
}

func singleAlignedZero() alignedZero {
	time.Sleep(time.Millisecond)
	return alignedZero{}
}

func printRecord(label string, out []reflect.Value, recordIndex int) {
	rec := out[recordIndex].Interface().(record)
	println(label, rec.Small, rec.Large)
}

func main() {
	captured := 7
	fn := func(value int, text string) (int, string) {
		time.Sleep(time.Millisecond)
		return captured + value, text + "!"
	}
	out := reflect.ValueOf(fn).Call([]reflect.Value{
		reflect.ValueOf(5),
		reflect.ValueOf("ok"),
	})
	println(out[0].Int(), out[1].String())

	out = reflect.ValueOf(named).Call([]reflect.Value{reflect.ValueOf(8)})
	println("named", out[0].Int())

	variadicValue := reflect.ValueOf(variadic)
	out = variadicValue.Call([]reflect.Value{
		reflect.ValueOf("call"),
		reflect.ValueOf(2),
		reflect.ValueOf(3),
	})
	printRecord("variadic-call", out, 1)
	println(out[2].String())
	out = variadicValue.CallSlice([]reflect.Value{
		reflect.ValueOf("slice"),
		reflect.ValueOf([]int{4, 5, 6}),
	})
	printRecord("variadic-slice", out, 1)
	println(out[2].String())

	c := &counter{value: 10}
	out = reflect.ValueOf(c).MethodByName("Add").Call(
		[]reflect.Value{reflect.ValueOf(5)},
	)
	printRecord("method", out, 1)

	bound := reflect.ValueOf(c).MethodByName("Add").Interface().(func(int) (empty, record))
	_, rec := bound(3)
	println("method-interface", rec.Small, rec.Large)

	var iface adder = c
	out = reflect.ValueOf(&iface).Elem().MethodByName("Add").Call(
		[]reflect.Value{reflect.ValueOf(7)},
	)
	printRecord("interface-method", out, 1)

	bound = reflect.ValueOf(&iface).Elem().MethodByName("Add").Interface().(func(int) (empty, record))
	_, rec = bound(2)
	println("interface-method-interface", rec.Small, rec.Large)

	method, ok := reflect.TypeOf(c).MethodByName("Add")
	if !ok {
		panic("method expression is absent")
	}
	out = method.Func.Call([]reflect.Value{
		reflect.ValueOf(c),
		reflect.ValueOf(4),
	})
	printRecord("method-expression", out, 1)

	out = reflect.ValueOf(alignedResults).Call(nil)
	println("aligned-zero", out[0].Uint(), out[1].Len(), out[2].Uint())
	out = reflect.ValueOf(singleAlignedZero).Call(nil)
	println("single-aligned-zero", out[0].Len())

	out = reflect.ValueOf(apply).Call([]reflect.Value{
		reflect.ValueOf(func(value int) int {
			time.Sleep(time.Millisecond)
			return value * 2
		}),
		reflect.ValueOf(6),
	})
	println("apply", out[0].Int())
}
