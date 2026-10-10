//go:build llgo

package llgoext

import "testing"

func TestLinknameNarrowCArguments(t *testing.T) {
	want := narrowValues{-8, 250, -300, 60000}
	if got := linkedNarrow(-8, 250, -300, 60000); got != want {
		t.Errorf("direct: got %+v, want %+v", got, want)
	}
	for _, call := range []struct {
		name string
		fn   narrowFunction
	}{
		{"converted-linkname", narrowFunction(linkedNarrow)},
		{"returned-C-pointer", linkedNarrowAddress()},
	} {
		t.Run(call.name, func(t *testing.T) {
			if got := call.fn(-8, 250, -300, 60000); got != want {
				t.Fatalf("got %+v, want %+v", got, want)
			}
			want := narrowValues{-128, 255, -32768, 65535}
			if got := call.fn(-128, 255, -32768, 65535); got != want {
				t.Fatalf("limits: got %+v, want %+v", got, want)
			}
		})
	}
}

func goNarrowS8(x int32) int8    { return int8(x) }
func goNarrowU8(x int32) uint8   { return uint8(x) }
func goNarrowS16(x int32) int16  { return int16(x) }
func goNarrowU16(x int32) uint16 { return uint16(x) }

func roundtripGoNarrowS8(fn func(int32) int8) narrowS8Callback {
	return linkedNarrowS8Roundtrip(narrowS8Callback(fn))
}

func TestLinknameNarrowCCallbacks(t *testing.T) {
	// A Go entry converted to a C function pointer still uses the Go return
	// convention. Returning it through C must not make the caller assume that
	// the entry has already sign- or zero-extended a narrow result.
	s8 := linkedNarrowS8Roundtrip(goNarrowS8)
	u8 := linkedNarrowU8Roundtrip(goNarrowU8)
	s16 := linkedNarrowS16Roundtrip(goNarrowS16)
	u16 := linkedNarrowU16Roundtrip(goNarrowU16)
	if got := int32(s8(248)); got != -8 {
		t.Errorf("int8 callback: got %d, want -8", got)
	}
	if got := uint32(u8(506)); got != 250 {
		t.Errorf("uint8 callback: got %d, want 250", got)
	}
	if got := int32(s16(65236)); got != -300 {
		t.Errorf("int16 callback: got %d, want -300", got)
	}
	if got := uint32(u16(125536)); got != 60000 {
		t.Errorf("uint16 callback: got %d, want 60000", got)
	}
	if got := int32(roundtripGoNarrowS8(goNarrowS8)(248)); got != -8 {
		t.Errorf("Go func value callback: got %d, want -8", got)
	}
}
