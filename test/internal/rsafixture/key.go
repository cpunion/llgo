// Package rsafixture supplies public, test-only RSA keys. Prime generation is
// tested separately; encrypt/sign/serialization tests need not repeat it.
package rsafixture

import (
	"crypto/rsa"
	"math/big"
)

// New returns an independent 2048-bit key. Keys 0 and 1 have two primes; key 2
// has three. Each call owns its integers and precomputation state so tests may
// mutate them without contaminating another test.
func New(index int) *rsa.PrivateKey {
	data := keyData[index]
	key := &rsa.PrivateKey{PublicKey: rsa.PublicKey{N: integer(data.n), E: 65537}, D: integer(data.d)}
	for _, prime := range data.primes {
		key.Primes = append(key.Primes, integer(prime))
	}
	return key
}

func integer(hex string) *big.Int {
	n, ok := new(big.Int).SetString(hex, 16)
	if !ok {
		panic("invalid RSA test fixture")
	}
	return n
}
