// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm

package math

const haveArchFloor = false

func archFloor(x float64) float64 {
	panic("not implemented")
}

const haveArchCeil = false

func archCeil(x float64) float64 {
	panic("not implemented")
}

const haveArchTrunc = false

func archTrunc(x float64) float64 {
	panic("not implemented")
}
