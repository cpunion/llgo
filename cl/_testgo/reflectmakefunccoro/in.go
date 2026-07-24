package main

import (
	"reflect"
	"time"
)

type record struct {
	Small int16
	Large int64
}

type alignedZero [0]int64

type scalarMatrixFunc func(
	bool, int8, uint16, float32, float64, complex64, complex128,
	string, any, [2]uint16, record,
) (
	bool, int64, uint64, float32, float64, complex64, complex128,
	string, any, [2]uint16, record,
)

func callAndRecover(fn func()) {
	defer func() {
		println(recover().(string))
	}()
	fn()
}

func main() {
	captured := 7
	value := reflect.MakeFunc(
		reflect.TypeOf((func(int, string) (int, string))(nil)),
		func(args []reflect.Value) []reflect.Value {
			time.Sleep(time.Millisecond)
			return []reflect.Value{
				reflect.ValueOf(captured + int(args[0].Int())),
				reflect.ValueOf(args[1].String() + "!"),
			}
		},
	)

	fn := value.Interface().(func(int, string) (int, string))
	number, text := fn(5, "ok")
	println(number, text)

	out := value.Call([]reflect.Value{
		reflect.ValueOf(6),
		reflect.ValueOf("go"),
	})
	println(out[0].Int(), out[1].String())

	withFunc := reflect.MakeFunc(
		reflect.TypeOf((func([]int, func(int) int) int)(nil)),
		func(args []reflect.Value) []reflect.Value {
			time.Sleep(time.Millisecond)
			n := args[0].Len()
			callback := args[1].Interface().(func(int) int)
			return []reflect.Value{reflect.ValueOf(callback(n))}
		},
	).Interface().(func([]int, func(int) int) int)
	println(withFunc([]int{1, 2, 3}, func(n int) int {
		time.Sleep(time.Millisecond)
		return n * 2
	}))

	variadic := reflect.MakeFunc(
		reflect.TypeOf((func(string, ...int) (record, string))(nil)),
		func(args []reflect.Value) []reflect.Value {
			values := args[1]
			var sum int64
			for i := 0; i < values.Len(); i++ {
				sum += values.Index(i).Int()
			}
			time.Sleep(time.Millisecond)
			return []reflect.Value{
				reflect.ValueOf(record{Small: int16(values.Len()), Large: sum}),
				reflect.ValueOf(args[0].String() + "?"),
			}
		},
	).Interface().(func(string, ...int) (record, string))
	rec, suffix := variadic("sum", 4, 5, 6)
	println(rec.Small, rec.Large, suffix)

	called := 0
	noResult := reflect.MakeFunc(
		reflect.TypeOf((func(int))(nil)),
		func(args []reflect.Value) []reflect.Value {
			called += int(args[0].Int())
			return nil
		},
	).Interface().(func(int))
	noResult(9)
	println(called)

	aligned := reflect.MakeFunc(
		reflect.TypeOf((func() (byte, alignedZero, byte))(nil)),
		func([]reflect.Value) []reflect.Value {
			time.Sleep(time.Millisecond)
			return []reflect.Value{
				reflect.ValueOf(byte(1)),
				reflect.ValueOf(alignedZero{}),
				reflect.ValueOf(byte(2)),
			}
		},
	).Interface().(func() (byte, alignedZero, byte))
	left, zero, right := aligned()
	println(left, len(zero), right)

	scalarValue := reflect.MakeFunc(
		reflect.TypeOf((scalarMatrixFunc)(nil)),
		func(args []reflect.Value) []reflect.Value {
			time.Sleep(time.Millisecond)
			if len(args) != 11 ||
				!args[0].Bool() ||
				args[1].Int() != -7 ||
				args[2].Uint() != 513 ||
				args[3].Float() != 1.5 ||
				args[4].Float() != -2.25 ||
				args[5].Complex() != complex(3, 4) ||
				args[6].Complex() != complex(-5, 6) ||
				args[7].String() != "matrix" ||
				args[8].Interface().(int) != 19 ||
				args[9].Index(0).Uint() != 21 ||
				args[9].Index(1).Uint() != 34 ||
				args[10].Field(0).Int() != 8 ||
				args[10].Field(1).Int() != 13 {
				panic("bad MakeFunc scalar matrix arguments")
			}
			return []reflect.Value{
				reflect.ValueOf(false),
				reflect.ValueOf(int64(-70)),
				reflect.ValueOf(uint64(5130)),
				reflect.ValueOf(float32(2.5)),
				reflect.ValueOf(float64(-3.25)),
				reflect.ValueOf(complex64(complex(4, 5))),
				reflect.ValueOf(complex128(complex(-6, 7))),
				reflect.ValueOf("matrix!"),
				reflect.ValueOf(any("interface-result")),
				reflect.ValueOf([2]uint16{55, 89}),
				reflect.ValueOf(record{Small: 21, Large: 34}),
			}
		},
	)
	scalar := scalarValue.Interface().(scalarMatrixFunc)
	checkScalarMatrix(scalar(
		true, -7, 513, 1.5, -2.25, complex(3, 4), complex(-5, 6),
		"matrix", 19, [2]uint16{21, 34}, record{Small: 8, Large: 13},
	))
	reflected := scalarValue.Call([]reflect.Value{
		reflect.ValueOf(true),
		reflect.ValueOf(int8(-7)),
		reflect.ValueOf(uint16(513)),
		reflect.ValueOf(float32(1.5)),
		reflect.ValueOf(float64(-2.25)),
		reflect.ValueOf(complex64(complex(3, 4))),
		reflect.ValueOf(complex128(complex(-5, 6))),
		reflect.ValueOf("matrix"),
		reflect.ValueOf(any(19)),
		reflect.ValueOf([2]uint16{21, 34}),
		reflect.ValueOf(record{Small: 8, Large: 13}),
	})
	checkScalarMatrix(
		reflected[0].Bool(),
		reflected[1].Int(),
		reflected[2].Uint(),
		float32(reflected[3].Float()),
		reflected[4].Float(),
		complex64(reflected[5].Complex()),
		reflected[6].Complex(),
		reflected[7].String(),
		reflected[8].Interface(),
		reflected[9].Interface().([2]uint16),
		reflected[10].Interface().(record),
	)
	println("scalar-matrix")

	panics := reflect.MakeFunc(
		reflect.TypeOf((func())(nil)),
		func([]reflect.Value) []reflect.Value {
			time.Sleep(time.Millisecond)
			panic("makefunc panic")
		},
	).Interface().(func())
	callAndRecover(panics)
}

func checkScalarMatrix(
	b bool, i int64, u uint64, f32 float32, f64 float64,
	c64 complex64, c128 complex128, text string, iface any,
	array [2]uint16, rec record,
) {
	if b ||
		i != -70 ||
		u != 5130 ||
		f32 != 2.5 ||
		f64 != -3.25 ||
		c64 != complex(4, 5) ||
		c128 != complex(-6, 7) ||
		text != "matrix!" ||
		iface.(string) != "interface-result" ||
		array != [2]uint16{55, 89} ||
		rec != (record{Small: 21, Large: 34}) {
		panic("bad MakeFunc scalar matrix results")
	}
}
