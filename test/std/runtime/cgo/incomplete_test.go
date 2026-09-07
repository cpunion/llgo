//go:build cgo

package cgo_test

import (
	"reflect"
	"runtime/cgo"
	"testing"
)

// Unlike Handle, Incomplete is declared in GOROOT's import-C file and does not
// exist in official Go builds with CGO_ENABLED=0, including both wasm profiles.
func TestIncompleteTypeIdentity(t *testing.T) {
	typ := reflect.TypeOf(cgo.Incomplete{})
	if typ.Name() != "Incomplete" || typ.PkgPath() != "runtime/cgo" {
		t.Fatalf("unexpected incomplete C type marker: %v from %q", typ, typ.PkgPath())
	}
}
