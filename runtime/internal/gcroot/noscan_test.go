package gcroot

import "testing"

func TestSplitStaticRoots(t *testing.T) {
	for _, test := range []struct {
		name                                      string
		start, end, skipStart, skipEnd, alignment uintptr
		before, after                             uintptr
	}{
		{"middle", 0, 64, 16, 48, 8, 16, 48},
		{"unaligned boundaries", 0, 64, 17, 47, 8, 24, 40},
		{"clip both", 16, 64, 0, 128, 8, 16, 64},
		{"empty", 0, 64, 16, 16, 8, 64, 64},
		{"before", 16, 64, 0, 16, 8, 64, 64},
		{"after", 0, 64, 64, 72, 8, 64, 64},
		{"partial only", 0, 64, 17, 23, 8, 64, 64},
		{"empty roots", 16, 16, 0, 32, 8, 16, 16},
		{"zero alignment", 0, 64, 16, 48, 0, 64, 64},
		{"non power alignment", 0, 64, 16, 48, 3, 64, 64},
		{"unaligned roots", 1, 64, 16, 48, 8, 64, 64},
		{"overflow", 0, ^uintptr(0), ^uintptr(0) - 1, ^uintptr(0), 8, ^uintptr(0), ^uintptr(0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			before, after := SplitStaticRoots(test.start, test.end, test.skipStart, test.skipEnd, test.alignment)
			if before != test.before || after != test.after {
				t.Fatalf("ranges end/start = %d/%d, want %d/%d", before, after, test.before, test.after)
			}
		})
	}
}
