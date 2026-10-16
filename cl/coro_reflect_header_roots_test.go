//go:build !llgo

package cl

import (
	"bytes"
	"go/types"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestCoroFrameReflectHeaderExactAliases(t *testing.T) {
	for _, test := range []struct {
		name, parameter, setup, middle string
	}{
		{"string-alias", "value string", "header := (*reflect.StringHeader)(unsafe.Pointer(&value))", ""},
		{"string-copy", "value string", "header := *(*reflect.StringHeader)(unsafe.Pointer(&value))", ""},
		{"slice-alias", "value []byte", "header := (*reflect.SliceHeader)(unsafe.Pointer(&value))", ""},
		{"slice-copy", "value []byte", "header := *(*reflect.SliceHeader)(unsafe.Pointer(&value))", ""},
		{"string-await", "value string, ready <-chan struct{}", "header := (*reflect.StringHeader)(unsafe.Pointer(&value))", "<-ready"},
		{"string-word-await", "value string, ready <-chan struct{}", "header := (*reflect.StringHeader)(unsafe.Pointer(&value))", "word := header.Data; <-ready"},
	} {
		t.Run(test.name, func(t *testing.T) {
			word := "header.Data"
			if test.name == "string-word-await" {
				word = "word"
			}
			source := "package foo\nimport (\"reflect\"; \"unsafe\")\nfunc Root(" + test.parameter + ") unsafe.Pointer {\n" +
				test.setup + "\n" + test.middle + "\nreturn unsafe.Pointer(" + word + ")\n}\n"
			prog, _, _, root, audit, proof := prepareCoroFrameRootAudit(t, source, "Root", EmissionUniverseOptions{})
			defer prog.Dispose()
			conversion := onlyReflectHeaderReconstruction(t, root)
			if !proof.provesTraceableUintptr(conversion.X) || audit.validateConvert(conversion) != "" {
				var dump bytes.Buffer
				ssa.WriteFunction(&dump, root)
				t.Fatalf("exact read-only header alias lacks frozen pointer provenance\n%s", dump.String())
			}
			roots := proof.uintptrValues[conversion.X].roots
			if len(roots) != 1 || roots[0] != root.Params[0] {
				t.Fatalf("header Data roots = %v, want original typed value %v", roots, root.Params[0])
			}
		})
	}
}

func TestCoroFrameReflectStringHeaderRejectsOpenForms(t *testing.T) {
	for _, test := range []struct{ name, declarations, body string }{
		{"mutated-data", "", "header := (*reflect.StringHeader)(unsafe.Pointer(&value)); header.Data = raw; return unsafe.Pointer(header.Data)"},
		{"mutated-source", "", "header := (*reflect.StringHeader)(unsafe.Pointer(&value)); value = other; return unsafe.Pointer(header.Data)"},
		{"escaped-source", "var escaped *string", "header := (*reflect.StringHeader)(unsafe.Pointer(&value)); escaped = &value; return unsafe.Pointer(header.Data)"},
		{"escaped-header", "var escaped *reflect.StringHeader", "header := (*reflect.StringHeader)(unsafe.Pointer(&value)); escaped = header; return unsafe.Pointer(header.Data)"},
		{"escaped-copy", "var escaped *reflect.StringHeader", "header := *(*reflect.StringHeader)(unsafe.Pointer(&value)); escaped = &header; return unsafe.Pointer(header.Data)"},
		{"unknown-header", "", "return unsafe.Pointer(external.Data)"},
		{"fabricated-header", "", "header := reflect.StringHeader{Data: raw, Len: len(value)}; return unsafe.Pointer(header.Data)"},
		{"lookalike", "type StringHeader struct { Data uintptr; Len int }", "header := (*StringHeader)(unsafe.Pointer(&value)); return unsafe.Pointer(header.Data)"},
		{"arithmetic", "", "header := (*reflect.StringHeader)(unsafe.Pointer(&value)); return unsafe.Pointer(header.Data + 1)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "package foo\nimport (\"reflect\"; \"unsafe\")\nvar _ reflect.StringHeader\n" + test.declarations +
				"\nfunc Root(value, other string, raw uintptr, external *reflect.StringHeader) unsafe.Pointer { " + test.body + " }\n"
			prog, _, _, root, audit, proof := prepareCoroFrameRootAudit(t, source, "Root", EmissionUniverseOptions{})
			defer prog.Dispose()
			conversion := onlyReflectHeaderReconstruction(t, root)
			if proof.provesTraceableUintptr(conversion.X) || audit.validateConvert(conversion) == "" {
				t.Fatal("open header or unknown uintptr acquired pointer provenance")
			}
		})
	}
}

func onlyReflectHeaderReconstruction(t *testing.T, fn *ssa.Function) *ssa.Convert {
	t.Helper()
	var found *ssa.Convert
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			conversion, ok := instruction.(*ssa.Convert)
			if !ok || !coroFrameRetentionUintptrLike(conversion.X.Type()) || !coroFrameRetentionPointerLike(conversion.Type()) {
				continue
			}
			if found != nil {
				t.Fatal("fixture has multiple uintptr reconstructions")
			}
			found = conversion
		}
	}
	if found == nil || !types.Identical(found.Type(), types.Typ[types.UnsafePointer]) {
		t.Fatal("fixture has no unsafe pointer reconstruction")
	}
	return found
}
