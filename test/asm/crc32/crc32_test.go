package crc32_test

import (
	"bytes"
	"testing"

	"github.com/klauspost/crc32"
)

func TestIndependentPolynomialDivision(t *testing.T) {
	for _, polynomial := range []uint32{0xedb88320, 0x82f63b78, 0xd5828281} {
		table := crc32.MakeTable(polynomial)
		for _, n := range []int{0, 1, 15, 16, 17, 63, 64, 65, 503, 504, 505, 1023, 1024, 1025, 4031, 4032, 4033, 65537} {
			storage := bytes.Repeat([]byte{0xa5}, n+4)
			input := storage[1 : n+1]
			for i := range input {
				input[i] = byte(i*131 + i/8 + 17)
			}
			original := bytes.Clone(storage)
			for _, initial := range []uint32{0, 0xffffffff, 0x12345678} {
				want := reference(initial, polynomial, input)
				if got := crc32.Update(initial, table, input); got != want {
					t.Fatalf("poly %08x length %d initial %08x: %08x, want %08x", polynomial, n, initial, got, want)
				}
			}
			hash := crc32.New(table)
			for start := 0; start < n; {
				end := min(start+509, n)
				if count, err := hash.Write(input[start:end]); err != nil || count != end-start {
					t.Fatalf("Write: %d, %v", count, err)
				}
				start = end
			}
			if hash.Sum32() != reference(0, polynomial, input) || !bytes.Equal(storage, original) {
				t.Fatal("streaming result or source guards differ")
			}
		}
	}
}

func reference(crc, polynomial uint32, input []byte) uint32 {
	crc = ^crc
	for _, value := range input {
		crc ^= uint32(value)
		for bit := 0; bit < 8; bit++ {
			low := crc & 1
			crc >>= 1
			if low != 0 {
				crc ^= polynomial
			}
		}
	}
	return ^crc
}
