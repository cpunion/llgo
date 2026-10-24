package gotest

import (
	"runtime"
	"testing"
)

type fieldGuardBox struct{ Value *int }

//go:noinline
func fieldGuardLoopStore(v *fieldGuardBox) int {
	if v == nil || v.Value == nil {
		return 0
	}
	n := 0
	for i := 0; i < 2; i++ {
		n += *v.Value
		v.Value = nil
	}
	return n
}

//go:noinline
func fieldGuardConditionalStore(v *fieldGuardBox) int {
	if v == nil || v.Value == nil {
		return 0
	}
	n := 0
	for i := 0; i < 2; i++ {
		n += *v.Value
		if i&1 == 0 {
			v.Value = nil
		}
	}
	return n
}

//go:noinline
func fieldGuardSavedStore(v *fieldGuardBox) int {
	if v == nil {
		return 0
	}
	nonNil := v.Value != nil
	v.Value = nil
	if nonNil {
		return *v.Value
	}
	return 0
}

//go:noinline
func fieldGuardSavedEarlier(v *fieldGuardBox, clear bool) int {
	if v == nil {
		return 0
	}
	nonNil := v.Value != nil
	if clear {
		v.Value = nil
	}
	if nonNil {
		return *v.Value
	}
	return 0
}

//go:noinline
func catchesFieldGuardPanic(fn func() int) (caught bool) {
	defer func() { caught = recover() != nil }()
	fn()
	return
}

func TestFieldNilGuardMemoryLifetime(t *testing.T) {
	for _, procs := range []int{1, 4} {
		old := runtime.GOMAXPROCS(procs)
		func() {
			defer runtime.GOMAXPROCS(old)
			for _, tc := range []struct {
				name string
				call func(*fieldGuardBox) int
			}{
				{"loop-store", fieldGuardLoopStore},
				{"conditional-loop-store", fieldGuardConditionalStore},
				{"store-before-branch", fieldGuardSavedStore},
				{"store-before-saved-branch", func(v *fieldGuardBox) int { return fieldGuardSavedEarlier(v, true) }},
			} {
				t.Run(tc.name, func(t *testing.T) {
					value := 7
					box := &fieldGuardBox{Value: &value}
					if !catchesFieldGuardPanic(func() int { return tc.call(box) }) {
						t.Fatal("expected recoverable nil dereference after field changed")
					}
				})
			}
		}()
	}
}
