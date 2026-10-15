//go:build llgo
// +build llgo

package test

import (
	"reflect"
	"testing"
	_ "unsafe"
)

//go:linkname funcPCABI0 llgo.funcPCABI0
func funcPCABI0(f interface{}) uintptr

func dynamicFuncPCABI0(f interface{}) uintptr {
	return funcPCABI0(f)
}

func TestFuncPCABI0(t *testing.T) {
	f0 := globalAdd
	f1 := makeNoFree()
	f2 := makeWithFree(5)
	f3 := makeWithFree(7)
	s := &S{v: 1}
	mv := s.Add
	me := (*S).Add

	cases := []struct {
		name string
		fn   interface{}
	}{
		{"global", f0},
		{"no-free", f1},
		{"free-5", f2},
		{"free-7", f3},
		{"method-value", mv},
		{"method-expr", me},
	}

	for _, tc := range cases {
		pc := dynamicFuncPCABI0(tc.fn)
		if pc == 0 {
			t.Fatalf("%s: FuncPCABI0 returned 0", tc.name)
		}
		if got := reflect.ValueOf(tc.fn).Pointer(); got != pc {
			t.Fatalf("%s: FuncPCABI0 mismatch: got=0x%x want=0x%x", tc.name, pc, got)
		}
	}

	if pc2, pc3 := dynamicFuncPCABI0(f2), dynamicFuncPCABI0(f3); pc2 != pc3 {
		t.Fatalf("free closure codeptr should match: pc2=0x%x pc3=0x%x", pc2, pc3)
	}
}
