//go:build !llgo

package cl

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestCoroPointerUintptrTypedGoCallIsOnlyScalarTerminal(t *testing.T) {
	for _, test := range []struct {
		name, declaration, parameters, body string
		want                                bool
		inverse                             bool
	}{
		{name: "identity", declaration: "func consume(v uintptr) uintptr { return v }", body: "_ = consume(word)", want: true},
		{name: "converted integer", declaration: "func consume(v uint64) {}", body: "consume(uint64(word))", want: true},
		{name: "suspending consumer", declaration: "var signal chan uintptr; func consume(v uintptr) { signal <- v }", body: "consume(word)", want: true},
		{name: "callee inverse rejected separately", declaration: "func consume(v uintptr) { _ = unsafe.Pointer(v) }", body: "consume(word)", want: true, inverse: true},
		{name: "caller inverse rejected separately", declaration: "func consume(v uintptr) uintptr { return v }", body: "_ = unsafe.Pointer(consume(word))", want: true, inverse: true},
		{name: "returned integer inverse after await", declaration: "var signal chan struct{}; func consume(v uintptr) uintptr { return v }", body: "returned := consume(word); <-signal; _ = unsafe.Pointer(returned)", want: true, inverse: true},
		{name: "returned bitcast inverse", declaration: "func consume(v uintptr) float64 { bits := uint64(v); return *(*float64)(unsafe.Pointer(&bits)) }; func inverse(v float64) uintptr { return uintptr(*(*uint64)(unsafe.Pointer(&v))) }", body: "_ = unsafe.Pointer(inverse(consume(word)))", want: true, inverse: true},
		{name: "returned bitcast inverse after await", declaration: "var signal chan struct{}; func consume(v uintptr) float64 { bits := uint64(v); return *(*float64)(unsafe.Pointer(&bits)) }; func inverse(v float64) uintptr { return uintptr(*(*uint64)(unsafe.Pointer(&v))) }", body: "returned := consume(word); <-signal; _ = unsafe.Pointer(inverse(returned))", want: true, inverse: true},
		{name: "returned integer to foreign address", declaration: "func consume(v uintptr) uintptr { return v }\n//go:linkname foreign C.consume_pointer\nfunc foreign(unsafe.Pointer)", body: "foreign(unsafe.Pointer(consume(word)))", want: true, inverse: true},
		{name: "returned integer to lifetime address", declaration: "func consume(v uintptr) uintptr { return v }\n//llgo:coro contract foreign.v1 scope=wrapper progress=executor-safe affinity=any-thread reentry=none memory=borrow-until-return\nfunc foreign(v unsafe.Pointer) {}", body: "foreign(unsafe.Pointer(consume(word)))", want: true, inverse: true},
		{name: "uintptr escapes", declaration: "//go:uintptrescapes\nfunc consume(v uintptr) {}", body: "consume(word)"},
		{name: "borrowed address contract", declaration: "//llgo:coro contract foreign.v1 scope=wrapper progress=executor-safe affinity=any-thread reentry=none memory=borrow-until-return\nfunc consume(v uintptr) {}", body: "consume(word)"},
		{name: "retained address contract", declaration: "//llgo:coro contract foreign.v1 scope=wrapper progress=executor-safe affinity=any-thread reentry=none memory=retained\nfunc consume(v uintptr) {}", body: "consume(word)"},
		{name: "foreign", declaration: "//go:linkname consume C.consume\nfunc consume(v uintptr)", body: "consume(word)"},
		{name: "unknown callable", parameters: ", consume func(uintptr)", body: "consume(word)"},
		{name: "boxed interface", declaration: "func consume(v any) {}", body: "consume(word)"},
		{name: "variadic", declaration: "func consume(v ...uintptr) {}", body: "consume(word)"},
		{name: "extra inverse use", declaration: "func consume(v uintptr) {}", body: "consume(word); _ = unsafe.Pointer(word)", inverse: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, universe, root, _, _ := prepareCoroFrameRootAudit(t, `package foo
import "unsafe"
`+test.declaration+`
func Root(pointer unsafe.Pointer`+test.parameters+`) {
	word := (uintptr(pointer) >> 13) << 13
	`+test.body+`
}
`, "Root", EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()})
			defer prog.Dispose()
			plan := analyzeCoroFrameRetentionFixture(t, pkg, universe, root, -1)
			audit, err := newCoroPhysicalPureSSAAudit(universe, plan, root, CoroFrameRetentionParkABIV2)
			if err != nil {
				t.Fatal(err)
			}
			var conversion *ssa.Convert
			for _, block := range root.Blocks {
				for _, instruction := range block.Instrs {
					if candidate, ok := instruction.(*ssa.Convert); ok && coroFrameRetentionPointerToUintptr(candidate) {
						conversion = candidate
					}
				}
			}
			if conversion == nil {
				t.Fatal("fixture has no pointer-derived integer")
			}
			proof := audit.currentFrameRetentionProof()
			before := len(proof.uintptrValues)
			if got := audit.coroPointerUintptrGoCallTerminal(conversion); got != test.want {
				t.Fatalf("typed Go integer call terminal = %v, want %v", got, test.want)
			}
			if len(proof.uintptrValues) != before || proof.provesTraceableUintptr(conversion) {
				t.Fatal("scalar call terminal manufactured inverse pointer provenance")
			}
			if test.want {
				if reason := audit.validateConvert(conversion); reason != "" {
					t.Fatalf("valid scalar call rejected: %s", reason)
				}
			}
			inverses := 0
			for _, fn := range []*ssa.Function{root, pkg.Func("consume")} {
				if fn == nil || len(fn.Blocks) == 0 {
					continue
				}
				calleeAudit, err := newCoroPhysicalPureSSAAudit(universe, plan, fn, CoroFrameRetentionParkABIV2)
				if err != nil {
					t.Fatal(err)
				}
				for _, block := range fn.Blocks {
					for _, instruction := range block.Instrs {
						inverse, ok := instruction.(*ssa.Convert)
						if !ok || !coroFrameRetentionUintptrLike(inverse.X.Type()) || !coroFrameRetentionPointerLike(inverse.Type()) {
							continue
						}
						inverses++
						if reason := calleeAudit.validateConvert(inverse); !strings.Contains(reason, "has no traceable exact pointer provenance") {
							t.Fatalf("independent inverse pointer gate was bypassed: %q", reason)
						}
					}
				}
			}
			if (inverses != 0) != test.inverse {
				t.Fatalf("inverse conversion count = %d, want present=%t", inverses, test.inverse)
			}
			if test.inverse {
				var dump strings.Builder
				root.WriteTo(&dump)
				t.Logf("scalar call accepts no inverse authority:\n%s", dump.String())
			}
		})
	}
}
