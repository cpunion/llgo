package main

import "reflect"

type receiver struct {
	base int
}

func (r receiver) Sum(a, b, c, d, e, f, g, h, i int) int {
	return r.base + a + b + c + d + e + f + g + h + i
}

func makeSum(base int) func(int, int, int, int, int, int, int, int, int) int {
	return func(a, b, c, d, e, f, g, h, i int) int {
		return base + a + b + c + d + e + f + g + h + i
	}
}

func makeFloatSum(base float64) func(float64, float64, float64, float64, float64, float64, float64, float64, float64) float64 {
	return func(a, b, c, d, e, f, g, h, i float64) float64 {
		return base + a + b + c + d + e + f + g + h + i
	}
}

func makeNestedSum(base int) func(int, int, int, int, int, int, int, int, int) int {
	return func(a, b, c, d, e, f, g, h, i int) int {
		args := []reflect.Value{
			reflect.ValueOf(a), reflect.ValueOf(b), reflect.ValueOf(c),
			reflect.ValueOf(d), reflect.ValueOf(e), reflect.ValueOf(f),
			reflect.ValueOf(g), reflect.ValueOf(h), reflect.ValueOf(i),
		}
		return int(reflect.ValueOf(makeSum(base)).Call(args)[0].Int())
	}
}

func intArgs() []reflect.Value {
	args := make([]reflect.Value, 9)
	for i := range args {
		args[i] = reflect.ValueOf(i + 1)
	}
	return args
}

func floatArgs() []reflect.Value {
	args := make([]reflect.Value, 9)
	for i := range args {
		args[i] = reflect.ValueOf(float64(i + 1))
	}
	return args
}

func checkInt(value reflect.Value, args []reflect.Value) {
	if got := value.Call(args)[0].Int(); got != 55 {
		panic(got)
	}
}

func main() {
	ints := intArgs()
	checkInt(reflect.ValueOf(makeSum(10)), ints)
	checkInt(reflect.ValueOf(makeNestedSum(10)), ints)

	ft := reflect.TypeOf(func(int, int, int, int, int, int, int, int, int) int { return 0 })
	made := reflect.MakeFunc(ft, func(args []reflect.Value) []reflect.Value {
		var sum int64 = 10
		for _, arg := range args {
			sum += arg.Int()
		}
		return []reflect.Value{reflect.ValueOf(int(sum))}
	})
	checkInt(made, ints)

	method := reflect.ValueOf(receiver{base: 10}).MethodByName("Sum")
	checkInt(method, ints)
	bound := method.Interface().(func(int, int, int, int, int, int, int, int, int) int)
	if got := bound(1, 2, 3, 4, 5, 6, 7, 8, 9); got != 55 {
		panic(got)
	}

	if got := reflect.ValueOf(makeFloatSum(10)).Call(floatArgs())[0].Float(); got != 55 {
		panic(got)
	}
	println("ok")
}
