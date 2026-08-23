//go:build !llgo

package cl

import (
	"strings"
	"testing"
)

func TestLargeAggregateCopyLowering(t *testing.T) {
	const globals = `
var source [65537]byte
var target [65537]byte
var other [65537]byte
`
	tests := []struct {
		name         string
		body         string
		wantMemmove  bool
		wantNilCheck bool
	}{
		{
			name:         "immediate pointer copy",
			body:         `func copy(dst, src *[65537]byte) { *dst = *src }`,
			wantMemmove:  true,
			wantNilCheck: true,
		},
		{
			name: "distinct global store",
			body: `func copy() {
				value := source
				other[0] = 1
				target = value
			}`,
			wantMemmove: true,
		},
		{
			name: "possibly aliasing store",
			body: `func copy(alias *[65537]byte) {
				value := source
				alias[0] = 1
				target = value
			}`,
		},
		{
			name: "source global mutation",
			body: `func copy() {
				value := source
				source[0] = 1
				target = value
			}`,
		},
		{
			name: "non-global source",
			body: `func copy(src, dst *[65537]byte) {
				value := *src
				other[0] = 1
				*dst = value
			}`,
		},
		{
			name: "synchronizing receive",
			body: `func copy(ch <-chan struct{}) {
				value := source
				<-ch
				target = value
			}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ir := compileWithRewrites(t, "package aggregatecopy\n"+globals+tt.body, nil)
			hasMemmove := strings.Contains(ir, "call void @llvm.memmove")
			hasAggregateLoad := strings.Contains(ir, "load [65537 x i8]")
			if tt.wantMemmove {
				if !hasMemmove || hasAggregateLoad {
					t.Fatalf("large copy was not lowered without an aggregate load:\n%s", ir)
				}
			} else if hasMemmove || !hasAggregateLoad {
				t.Fatalf("order-sensitive large copy was unexpectedly deferred:\n%s", ir)
			}
			if tt.wantNilCheck && !strings.Contains(ir, "AssertNilDeref") {
				t.Fatalf("lowered pointer copy lost its source nil check:\n%s", ir)
			}
		})
	}
}

func TestSmallAggregateCopyRemainsDirect(t *testing.T) {
	ir := compileWithRewrites(t, `package aggregatecopy

var source [8]byte
var target [8]byte

func copy() { target = source }
`, nil)
	if strings.Contains(ir, "call void @llvm.memmove") {
		t.Fatalf("small aggregate copy was unnecessarily lowered:\n%s", ir)
	}
}
