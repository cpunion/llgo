package main

import (
	"math"
	"syscall"
)

func same(a, b float64) bool {
	return math.Float64bits(a) == math.Float64bits(b)
}

func main() {
	negativeZero := math.Copysign(0, -1)
	for _, value := range []float64{0, negativeZero, math.Inf(1), math.Inf(-1)} {
		if !same(math.Floor(value), value) || !same(math.Ceil(value), value) || !same(math.Trunc(value), value) {
			panic("math special value sign changed")
		}
	}
	if !math.IsNaN(math.Floor(math.NaN())) || !math.IsNaN(math.Ceil(math.NaN())) || !math.IsNaN(math.Trunc(math.NaN())) {
		panic("math NaN changed")
	}
	for _, test := range []struct{ value, floor, ceil, trunc float64 }{
		{1.5, 1, 2, 1}, {-1.5, -2, -1, -1}, {2, 2, 2, 2}, {-2, -2, -2, -2},
		{math.SmallestNonzeroFloat64, 0, 1, 0},
		{-math.SmallestNonzeroFloat64, -1, negativeZero, negativeZero},
	} {
		if !same(math.Floor(test.value), test.floor) || !same(math.Ceil(test.value), test.ceil) || !same(math.Trunc(test.value), test.trunc) {
			panic("math rounding mismatch")
		}
	}
	const success = "WASM generic math semantics ok\n"
	if n, err := syscall.Write(1, []byte(success)); err != nil || n != len(success) {
		panic("math completion marker")
	}
}
