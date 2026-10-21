// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package gotest

import (
	"io"
	"math"
	"testing"
)

//go:noinline
func dynamicInterfaceEqual(a, b any) bool { return a == b }

//go:noinline
func dynamicInterfaceNotEqual(a, b any) bool { return a != b }

//go:noinline
func dynamicErrorEqual(a, b error) bool { return a == b }

//go:noinline
func dynamicErrorNotEqual(a, b error) bool { return a != b }

type interfaceCompareError struct{}

func (*interfaceCompareError) Error() string { return "pointer error" }

type interfaceCompareSlice []int

func (interfaceCompareSlice) Error() string { return "slice error" }

func TestDynamicInterfaceNilComparison(t *testing.T) {
	var pointer *int
	var function func()
	var slice []int
	var mapping map[int]int
	nan := math.NaN()
	for _, test := range []struct {
		name string
		a, b any
		want bool
	}{
		{"nil-nil", nil, nil, true},
		{"nil-int", nil, 1, false},
		{"nil-pointer", nil, pointer, false},
		{"nil-function", nil, function, false},
		{"nil-slice", nil, slice, false},
		{"nil-map", nil, mapping, false},
		{"nil-live-slice", nil, []int{1}, false},
		{"nil-live-map", nil, map[int]int{1: 1}, false},
		{"same-typed-nil", pointer, pointer, true},
		{"different-typed-nil", pointer, (*string)(nil), false},
		{"same-value", 42, 42, true},
		{"different-type", 42, int64(42), false},
		{"different-value", 42, 43, false},
		{"nan", nan, nan, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, operands := range [][2]any{{test.a, test.b}, {test.b, test.a}} {
				if got := dynamicInterfaceEqual(operands[0], operands[1]); got != test.want {
					t.Fatalf("equal=%v want=%v", got, test.want)
				}
				if got := dynamicInterfaceNotEqual(operands[0], operands[1]); got == test.want {
					t.Fatalf("not equal=%v equal expectation=%v", got, test.want)
				}
			}
		})
	}
}

func TestDynamicErrorNilComparison(t *testing.T) {
	var pointer *interfaceCompareError
	var slice interfaceCompareSlice
	for _, test := range []struct {
		a, b error
		want bool
	}{
		{nil, nil, true}, {nil, io.EOF, false}, {io.EOF, nil, false},
		{pointer, nil, false}, {nil, pointer, false}, {pointer, pointer, true},
		{slice, nil, false}, {nil, slice, false},
		{io.EOF, io.EOF, true}, {io.EOF, io.ErrUnexpectedEOF, false},
	} {
		if got := dynamicErrorEqual(test.a, test.b); got != test.want {
			t.Fatalf("equal=%v want=%v", got, test.want)
		}
		if got := dynamicErrorNotEqual(test.a, test.b); got == test.want {
			t.Fatalf("not equal=%v equal expectation=%v", got, test.want)
		}
	}
}

func TestDynamicInterfaceUncomparablePanics(t *testing.T) {
	for _, value := range []any{[]int(nil), []int{1}, map[int]int(nil), map[int]int{1: 1}, (func())(nil)} {
		for _, compare := range []func(any, any) bool{dynamicInterfaceEqual, dynamicInterfaceNotEqual} {
			panicked := false
			func() {
				defer func() { panicked = recover() != nil }()
				compare(value, value)
			}()
			if !panicked {
				t.Fatal("same non-comparable dynamic type did not panic")
			}
		}
	}
	for _, compare := range []func(error, error) bool{dynamicErrorEqual, dynamicErrorNotEqual} {
		panicked := false
		func() {
			defer func() { panicked = recover() != nil }()
			compare(interfaceCompareSlice(nil), interfaceCompareSlice(nil))
		}()
		if !panicked {
			t.Fatal("non-comparable error did not panic")
		}
	}
}

func TestDynamicInterfaceComparisonLoopAndOrder(t *testing.T) {
	var events []int
	operand := func(tag int, value any) any {
		events = append(events, tag)
		return value
	}
	if dynamicInterfaceEqual(operand(1, nil), operand(2, []int(nil))) || len(events) != 2 || events[0] != 1 || events[1] != 2 {
		t.Fatal("operand evaluation changed", events)
	}
	left, right := [32]any{nil, 1, (*int)(nil)}, [32]any{nil, 1, (*int)(nil)}
	for range 100000 {
		if left != right || !dynamicInterfaceEqual(nil, nil) || dynamicErrorEqual(nil, io.EOF) {
			t.Fatal("comparison loop or aggregate PHI changed")
		}
	}
}
