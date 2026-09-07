package rsafixture_test

import (
	"testing"

	"github.com/xgo-dev/llgo/test/internal/rsafixture"
)

func TestRSAFixtureValidationAndIndependence(t *testing.T) {
	for i, primes := range []int{2, 2, 3} {
		key := rsafixture.New(i)
		if key.N.BitLen() != 2048 || len(key.Primes) != primes {
			t.Fatalf("key %d has %d bits and %d primes", i, key.N.BitLen(), len(key.Primes))
		}
		if err := key.Validate(); err != nil {
			t.Fatalf("key %d: %v", i, err)
		}
		key.D.SetInt64(0)
		if rsafixture.New(i).D.Sign() <= 0 {
			t.Fatal("fixture calls share mutable integers")
		}
	}
	if rsafixture.New(0).Equal(rsafixture.New(1)) {
		t.Fatal("fixtures for distinct-key comparisons are equal")
	}
}
