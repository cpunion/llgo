package wasmtest

import (
	"runtime"
	"strings"
	"testing"
)

func TestRangeIteratorGCRoots(t *testing.T) {
	input := strings.Repeat("a界🙂é", 2)
	var output []byte
	for _, r := range input {
		runtime.GC()
		output = append(output, string(r)...)
	}
	if string(output) != input {
		t.Fatalf("string iterator lost state across GC: %q, want %q", output, input)
	}
	m := make(map[int]*int)
	for i := 0; i < 4; i++ {
		value := new(int)
		*value = i + 1
		m[i] = value
	}
	seen := 0
	for key, value := range m {
		runtime.GC()
		if *value != key+1 {
			t.Fatalf("map iterator lost a live value: key=%d value=%d", key, *value)
		}
		seen |= 1 << key
	}
	if seen != 15 {
		t.Fatalf("map iterator lost state across GC: visited mask=%b", seen)
	}
}
