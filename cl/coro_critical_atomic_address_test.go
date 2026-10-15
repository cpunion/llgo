package cl

import (
	"strings"
	"testing"
)

const coroCriticalAtomicAddressPreamble = `package foo
import "unsafe"
//go:linkname enter llgo.coroCriticalPin
func enter() int
//go:linkname exit llgo.coroCriticalExit
func exit()
//go:linkname load llgo.atomicLoad
func load(*uint32) uint32
//go:linkname store llgo.atomicStore
func store(*uint32, uint32)
//go:linkname cas llgo.atomicCmpXchgOK
func cas(*uint32, uint32, uint32) bool
//go:linkname xchg llgo.atomicXchg
func xchg(*uint32, uint32) uint32
//go:linkname loadRaw llgo.atomicLoadUnsafe
func loadRaw(unsafe.Pointer) unsafe.Pointer
//go:linkname storeRaw llgo.atomicStoreUnsafe
func storeRaw(unsafe.Pointer, unsafe.Pointer)
//go:linkname storePointer llgo.atomicStore
func storePointer(*unsafe.Pointer, unsafe.Pointer)
type cells struct { A, B uint32 }
var cell uint32
var ptr unsafe.Pointer
`

func TestCoroCriticalAtomicAddressRejectsNullableOperands(t *testing.T) {
	for _, test := range []struct {
		name, parameter, operation string
	}{
		{"load", "p *uint32", "load(p)"},
		{"store", "p *uint32", "store(p, 1)"},
		{"cas", "p *uint32", "cas(p, 0, 1)"},
		{"xchg", "p *uint32", "xchg(p, 1)"},
		{"raw-load", "p unsafe.Pointer", "loadRaw(p)"},
		{"raw-store", "p unsafe.Pointer", "storeRaw(p, nil)"},
		{"typed-pointer-store", "p *unsafe.Pointer", "storePointer(p, nil)"},
		{"nil", "", "load(nil)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := coroCriticalAtomicAddressPreamble + "func Root(" + test.parameter + ") { enter(); " + test.operation + "; exit() }"
			_, err := proveCoroCriticalFixture(t, source)
			if err == nil || !strings.Contains(err.Error(), "atomic address lacks an exact non-nil proof") {
				t.Fatalf("critical atomic proof error = %v", err)
			}
		})
	}
}

func TestCoroCriticalAtomicAddressUsesSharedNonNilProof(t *testing.T) {
	for _, test := range []struct{ name, body string }{
		{"guarded-operations", `func Root(p *uint32) {
if p == nil { return }; enter(); load(p); store(p, 1); cas(p, 1, 2); xchg(p, 3); exit()
}`},
		{"guarded-raw", `func Root(p unsafe.Pointer) {
if p == nil { return }; enter(); loadRaw(p); storeRaw(p, nil); exit()
}`},
		{"converted", `func Root(p *uint32) {
if p == nil { return }; q := (*uint32)(unsafe.Pointer(p)); enter(); load(q); exit()
}`},
		{"global", `func Root() {
enter(); load(&cell); store(&cell, 1); cas(&cell, 1, 2); xchg(&cell, 3); storePointer(&ptr, nil); exit()
}`},
		{"global-raw", `func Root() { enter(); loadRaw(unsafe.Pointer(&ptr)); storeRaw(unsafe.Pointer(&ptr), nil); exit() }`},
		{"local", `func Root() { var c uint32; enter(); load(&c); exit() }`},
		{"completed-field", `func Root(p *cells) {
q := &p.A; enter(); load(q); store(q, 1); exit()
}`},
		{"guarded-field", `func Root(p *cells) {
if p == nil { return }; enter(); store(&p.A, 1); load(&p.B); exit()
}`},
		{"completed-array-element", `func Root(i int) {
var a [2]uint32; p := &a[i]; enter(); load(p); exit()
}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			proof, err := proveCoroCriticalFixture(t, coroCriticalAtomicAddressPreamble+test.body)
			if err != nil || proof == nil {
				t.Fatalf("critical atomic proof = %v, %v", proof, err)
			}
		})
	}
}

func TestCoroCriticalAtomicAddressRejectsUnprovedFieldAndBypassedGuard(t *testing.T) {
	for _, test := range []struct{ name, body string }{
		{"field-inside", `func Root(p *cells) { enter(); load(&p.A); exit() }`},
		{"bypassed-guard", `func Root(p *uint32, flag bool) { if flag && p == nil { return }; enter(); load(p); exit() }`},
		{"different-address", `func Root(p, q *uint32) { if p == nil { return }; enter(); load(q); exit() }`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := proveCoroCriticalFixture(t, coroCriticalAtomicAddressPreamble+test.body); err == nil {
				t.Fatal("critical atomic accepted a potentially faulting address")
			}
		})
	}
}
