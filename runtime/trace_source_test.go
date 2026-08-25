package runtime_test

import (
	"os"
	"strings"
	"testing"
)

func TestTraceReadBoundaryIsManagedGo(t *testing.T) {
	source, err := os.ReadFile("internal/lib/runtime/trace_stub_llgo.go")
	if err != nil {
		t.Fatal(err)
	}
	const marker = "//llgo:managedlink\n//go:linkname runtime_readTrace runtime/trace.runtime_readTrace"
	if !strings.Contains(string(source), marker) {
		t.Fatalf("trace read boundary lacks exact managed-Go contract %q", marker)
	}
}
