package huff0_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/klauspost/compress/huff0"
)

// These tables and bitstreams are built directly from the Zstandard Huffman
// format, not by Compress1X/Compress4X. Weights [1,1] give one-bit codes 0,1.
// Weights [1,1,2,3,4,5,6,7,8,9] give code 0 for symbol 0 and code 1 for every
// other symbol, with lengths 9,9,8,7,6,5,4,3,2,1. The last weight is implicit.
// Keeping the two table logs exercises both ARM64 four-stream assembly loops.
func TestIndependentHuffmanStreams(t *testing.T) {
	for _, table := range []struct {
		name    string
		weights []byte
		header  []byte
		log     int
	}{
		{"log1", []byte{1, 1}, []byte{128, 0x10}, 1},
		{"log9", []byte{1, 1, 2, 3, 4, 5, 6, 7, 8, 9}, []byte{136, 0x11, 0x23, 0x45, 0x67, 0x80}, 9},
	} {
		t.Run(table.name, func(t *testing.T) {
			plain := make([]byte, 8192)
			for i := range plain {
				plain[i] = byte(i % len(table.weights))
			}
			scratch, remain, err := huff0.ReadTable(table.header, nil)
			if err != nil || len(remain) != 0 {
				t.Fatalf("ReadTable: remaining %d, %v", len(remain), err)
			}
			decoder := scratch.Decoder()
			one := makeStream(plain, table.weights, table.log)
			got, err := decoder.Decompress1X(make([]byte, 0, len(plain)), one)
			if err != nil || !bytes.Equal(got, plain) {
				t.Fatalf("independent 1X stream: %v, output length %d", err, len(got))
			}

			four := make([]byte, 6)
			for i := 0; i < 4; i++ {
				part := makeStream(plain[i*2048:(i+1)*2048], table.weights, table.log)
				if i < 3 {
					binary.LittleEndian.PutUint16(four[i*2:], uint16(len(part)))
				}
				four = append(four, part...)
			}
			for _, offset := range []int{0, 1, 3} {
				storage := bytes.Repeat([]byte{0xa5}, len(plain)+offset+1)
				dst := storage[offset:offset:offset+len(plain)]
				got, err := decoder.Decompress4X(dst, four)
				if err != nil || !bytes.Equal(got, plain) {
					t.Fatalf("independent 4X stream offset %d: %v, output length %d", offset, err, len(got))
				}
				if storage[len(storage)-1] != 0xa5 || offset != 0 && storage[offset-1] != 0xa5 {
					t.Fatal("decoder overwrote a destination guard")
				}
			}
			if _, err := decoder.Decompress4X(make([]byte, 0, len(plain)), four[:9]); err == nil {
				t.Fatal("truncated four-stream input was accepted")
			}
		})
	}
}

func makeStream(plain, weights []byte, tableLog int) []byte {
	var out []byte
	bit := 0
	put := func(value byte) {
		if bit%8 == 0 {
			out = append(out, 0)
		}
		out[bit/8] |= value << (bit % 8)
		bit++
	}
	// Huffman streams are consumed from the end. The end marker is the highest
	// set bit in the final byte; it is not part of the decoded message.
	for i := len(plain) - 1; i >= 0; i-- {
		code := byte(1)
		if plain[i] == 0 {
			code = 0
		}
		length := tableLog + 1 - int(weights[plain[i]])
		for j := 0; j < length; j++ {
			put((code >> j) & 1)
		}
	}
	put(1)
	return out
}
