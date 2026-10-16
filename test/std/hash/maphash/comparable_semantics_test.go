package maphash_test

import (
	"hash/maphash"
	"math"
	"testing"
)

func equalComparableHashes[T comparable](t *testing.T, left, right T) {
	t.Helper()
	if left != right {
		t.Fatal("invalid equal-value hash fixture")
	}
	seed := maphash.MakeSeed()
	if maphash.Comparable(seed, left) != maphash.Comparable(seed, right) {
		t.Fatal("equal comparable values have different hashes")
	}
}

func TestComparableValueSemantics(t *testing.T) {
	equalComparableHashes(t, 0.0, math.Copysign(0, -1))
	equalComparableHashes(t, complex(0.0, 0), complex(math.Copysign(0, -1), 0))
	equalComparableHashes(t, [2]string{"same", "values"}, [2]string{"same", "values"})
	type record struct {
		Flag bool
		Text string
		Real float64
	}
	equalComparableHashes(t, record{true, "payload", 0}, record{true, string([]byte("payload")), math.Copysign(0, -1)})
	var nilInterface any
	equalComparableHashes(t, nilInterface, nilInterface)
	equalComparableHashes[any](t, 7, 7)
	var nilPointer *int
	equalComparableHashes[any](t, nilPointer, nilPointer)
}

func TestComparableUnhashableInterfacePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("hashing an interface containing a slice did not panic")
		}
	}()
	maphash.Comparable[any](maphash.MakeSeed(), []int{1})
}
