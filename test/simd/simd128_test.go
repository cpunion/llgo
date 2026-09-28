//go:build goexperiment.simd && (amd64 || arm64 || wasm)

package simd_test

import (
	"simd/archsimd"
	"testing"
)

//go:noinline
func add(x, y archsimd.Float32x4) archsimd.Float32x4 { return x.Add(y) }

//go:noinline
func lane(x archsimd.Float32x4, i uint8) float32 { return x.GetElem(i) }

//go:noinline
func indirect(f func(archsimd.Float32x4, archsimd.Float32x4) archsimd.Float32x4, x, y archsimd.Float32x4) archsimd.Float32x4 {
	return f(x, y)
}

func TestFloatArithmeticAndCalls(t *testing.T) {
	var x, y archsimd.Float32x4
	x = x.SetElem(0, 1.5).SetElem(3, -2)
	y = y.SetElem(0, 2.5).SetElem(3, 5)
	z := add(x, y)
	if lane(z, 0) != 4 || lane(z, 1) != 0 || lane(z, 3) != 3 {
		t.Fatal("float add")
	}
	viaExpr := indirect(archsimd.Float32x4.Add, x, y)
	if viaExpr.GetElem(0) != 4 {
		t.Fatal("method expression")
	}
	go z.Add(y)
	done := make(chan float32, 1)
	go func(f func(uint8) float32) { done <- f(0) }(z.GetElem)
	if <-done != 4 {
		t.Fatal("goroutine method value")
	}
	sub := z.Sub
	z = sub(y)
	if lane(z, 0) != 1.5 || lane(z, 3) != -2 {
		t.Fatal("method value")
	}
	var d archsimd.Float64x2
	d = d.SetElem(0, -1.25).SetElem(1, 2.5)
	d = d.Add(d)
	if d.GetElem(0) != -2.5 || d.GetElem(1) != 5 {
		t.Fatal("float64 lanes")
	}
}

func TestIntegerArithmeticAndBits(t *testing.T) {
	var a, b archsimd.Uint64x2
	a = a.SetElem(0, ^uint64(0)).SetElem(1, 7)
	b = b.SetElem(0, 1).SetElem(1, 3)
	c := a.Add(b)
	if c.GetElem(0) != 0 || c.GetElem(1) != 10 {
		t.Fatal("integer wrap")
	}
	c = a.Xor(b)
	if c.GetElem(0) != ^uint64(1) || c.GetElem(1) != 4 {
		t.Fatal("xor")
	}
	c = a.And(b).Or(b)
	if c.GetElem(0) != 1 || c.GetElem(1) != 3 {
		t.Fatal("and/or")
	}
	var small archsimd.Int8x16
	for i := uint8(0); i < 16; i++ {
		small = small.SetElem(i, int8(i)+120)
	}
	small = small.Add(small)
	for i := uint8(0); i < 16; i++ {
		want := int8(i) + 120
		if got := small.GetElem(i); got != want+want {
			t.Fatalf("lane %d: got %d, want %d", i, got, want+want)
		}
	}
}

func TestLaneBounds(t *testing.T) {
	var z archsimd.Float32x4
	for _, deferred := range []bool{false, true} {
		name := "direct"
		if deferred {
			name = "deferred"
		}
		t.Run(name, func(t *testing.T) {
			defer func() {
				err, ok := recover().(interface{ Error() string })
				if !ok || err.Error() != "runtime error: out-of-range immediate for simd intrinsic" {
					t.Fatalf("unexpected recovered error: %v", err)
				}
			}()
			if deferred {
				defer z.GetElem(255)
				return
			}
			lane(z, 4)
		})
	}
}
