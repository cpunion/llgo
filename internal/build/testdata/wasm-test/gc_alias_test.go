package wasmtest

import (
	"runtime"
	"testing"
)

type gcAliasItem struct {
	value *int
	pad   [63]uintptr
}

//go:noinline
func collectGCInteriorAliases(base *[4]gcAliasItem) int {
	a, b, c, d := &base[0].value, &base[1].value, &base[2].value, &base[3].value
	collectGCAggregate()
	runtime.KeepAlive(base)
	// Only the interior pointers are used after collection and suspension.
	return **a + **b + **c + **d
}

func TestInteriorAliasGCRoots(t *testing.T) {
	for iteration := 0; iteration < 16; iteration++ {
		base := new([4]gcAliasItem)
		for i := range base {
			base[i].value = new(int)
			*base[i].value = iteration + i
		}
		if got, want := collectGCInteriorAliases(base), iteration*4+6; got != want {
			t.Fatalf("interior aliases = %d, want %d", got, want)
		}
	}
	// A loop's allocation instruction can denote a different object on each
	// iteration. An interior pointer from the previous iteration stays live.
	var previous *gcAliasItem
	for i := 0; i < 16; i++ {
		base := new([4]gcAliasItem)
		current := &base[3]
		current.value = new(int)
		*current.value = i
		collectGCAggregate()
		if previous != nil && *previous.value != i-1 {
			t.Fatal("loop allocation replaced the previous interior root")
		}
		runtime.KeepAlive(base)
		previous = current
	}
}
