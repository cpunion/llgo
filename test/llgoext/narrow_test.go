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
