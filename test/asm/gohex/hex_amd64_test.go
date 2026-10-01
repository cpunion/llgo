package gohex_test

import (
	"bytes"
	"testing"

	hex "github.com/tmthrgd/go-hex"
	"golang.org/x/sys/cpu"
)

func TestAssemblyHexIndependentAlphabet(t *testing.T) {
	if !cpu.X86.HasAVX && !cpu.X86.HasSSE41 {
		t.Fatal("assembly oracle requires AVX or SSE4.1")
	}
	t.Logf("public dispatch: AVX=%t SSE4.1=%t", cpu.X86.HasAVX, cpu.X86.HasSSE41)
	for _, n := range []int{1, 15, 16, 17, 31, 32, 33, 4097} {
		plain := make([]byte, n)
		for i := range plain {
			plain[i] = byte(i*131 + 17)
		}
		for _, alphabet := range []string{"0123456789abcdef", "0123456789ABCDEF"} {
			want := make([]byte, n*2)
			for i, value := range plain {
				want[2*i], want[2*i+1] = alphabet[value>>4], alphabet[value&15]
			}
			storage := bytes.Repeat([]byte{0xa5}, 2*n+2)
			encoded := storage[1 : 2*n+1]
			if hex.RawEncode(encoded, plain, []byte(alphabet)) != 2*n || !bytes.Equal(encoded, want) {
				t.Fatalf("encode length %d alphabet %s", n, alphabet)
			}
			decoded := make([]byte, n+2)
			decoded[0], decoded[n+1] = 0xa5, 0xa5
			count, err := hex.Decode(decoded[1:n+1], want)
			if err != nil || count != n || !bytes.Equal(decoded[1:n+1], plain) {
				t.Fatalf("decode independently encoded length %d: count %d, %v", n, count, err)
			}
			if storage[0] != 0xa5 || storage[len(storage)-1] != 0xa5 || decoded[0] != 0xa5 || decoded[n+1] != 0xa5 {
				t.Fatal("hex assembly overwrote a guard")
			}
		}
	}
	if _, err := hex.DecodeString("012g"); err == nil {
		t.Fatal("invalid hex byte accepted")
	}
	if _, err := hex.DecodeString("012"); err == nil {
		t.Fatal("odd-length hex accepted")
	}
}
