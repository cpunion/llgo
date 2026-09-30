//go:build go1.21

package runtime_test

import (
	"runtime"
	"testing"
	_ "unsafe"
)

//go:linkname runtimeGetAuxv runtime.getAuxv
func runtimeGetAuxv() []uintptr

func TestRuntimeGetAuxvLinknameContract(t *testing.T) {
	first := runtimeGetAuxv()
	second := runtimeGetAuxv()
	if len(first)%2 != 0 {
		t.Fatalf("auxv has %d entries, want tag/value pairs", len(first))
	}
	if len(first) != len(second) || cap(first) != cap(second) {
		t.Fatal("getAuxv changed its slice header between calls")
	}
	if len(first) != 0 && &first[0] != &second[0] {
		t.Fatal("getAuxv did not retain the process auxiliary vector")
	}
	for i := 0; i < len(first); i += 2 {
		if first[i] == 0 {
			t.Fatalf("auxv includes AT_NULL terminator at pair %d", i/2)
		}
	}
	switch runtime.GOOS {
	case "darwin", "ios", "windows", "js", "wasip1", "plan9":
		if first != nil {
			t.Fatalf("getAuxv on %s = %v, want nil", runtime.GOOS, first)
		}
	}
}
