package build

import (
	"bytes"
	stdctx "context"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Compile the production helpers with a counted store resolver. The stand-in
// deliberately does not declare the GLS global: update helpers must work with
// the resolved store, not independently access goroutine-local state again.
func TestWasmCallerStoreUpdates(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join("..", "..", "runtime", "internal", "runtime", "caller.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	functions := map[string]int{
		"RecordCallerLocation": 1, "RecordPanicLocation": 1,
		"updateCurrentFrame": 1, "recordPCLocation": 2,
	}
	var source bytes.Buffer
	source.WriteString(callerStoreUpdateSource)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || functions[fn.Name.Name] == 0 {
			continue
		}
		if err := format.Node(&source, fset, fn); err != nil {
			t.Fatal(err)
		}
		source.WriteByte('\n')
		functions[fn.Name.Name]--
	}
	for name, remaining := range functions {
		if remaining != 0 {
			t.Fatalf("missing caller update function %s", name)
		}
	}
	path := filepath.Join(t.TempDir(), "caller_test.go")
	if err := os.WriteFile(path, source.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-count=1", "-timeout=20s", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("actual caller update helpers: %v\n%s", err, output)
	}
}

const callerStoreUpdateSource = `package caller
import "testing"
type CallerFrame struct {
  PC, Entry uintptr
  Function, File string
  Line, StartLine int
  captured uintptr
}
type callerLocationStore struct { frames, stack []CallerFrame }
const callerLocationLimit = 4096
var current *callerLocationStore
var lookups int
func callerLocationStoreForGoroutine() *callerLocationStore {
  lookups++
  if current == nil { current = new(callerLocationStore) }
  return current
}
func TestSourceUpdates(t *testing.T) {
  for _, record := range []func(uintptr, string, string, int){RecordCallerLocation, RecordPanicLocation} {
    current, lookups = nil, 0
    record(0, "f", "a.go", 1)
    record(7, "f", "a.go", 0)
    record(7, "f", "a.go", -1)
    if current != nil || lookups != 0 { t.Fatal("invalid site bootstrapped a store") }
    record(7, "f", "a.go", 1)
    if lookups != 1 || len(current.frames) != 1 { t.Fatal("cold update") }
    outer := CallerFrame{Entry:7, Function:"f", File:"a.go", Line:1, captured:4}
    current.stack = []CallerFrame{outer, outer}
    for _, site := range []struct { name, file string; line int; invalidate bool }{
      {"f", "a.go", 1, false},
      {"f", "a.go", 2, true},
      {"f", "b.go", 2, true},
      {"renamed", "b.go", 2, true},
    } {
      current.stack[1].captured = 8
      lookups = 0
      record(7, site.name, site.file, site.line)
      frame := current.stack[1]
      if lookups != 1 { t.Fatalf("one source update resolved GLS %d times", lookups) }
      if frame.Function != site.name || frame.File != site.file || frame.Line != site.line ||
        (frame.captured == 0) != site.invalidate { t.Fatalf("top frame: %+v", frame) }
      if current.stack[0] != outer { t.Fatal("recursive update changed the outer frame") }
      if len(current.frames) != 1 || current.frames[0].Line != site.line ||
        current.frames[0].File != site.file || current.frames[0].Function != site.name {
        t.Fatal("historical lookup lost the latest site")
      }
    }
    record(99, "other", "b.go", 10)
    if current.stack[0] != outer || current.stack[1].Entry != 7 || len(current.frames) != 2 {
      t.Fatal("unmatched entry changed the active stack")
    }
  }
  var missing *callerLocationStore
  missing.updateCurrentFrame(7, "f", "a.go", 1)
}
func TestPCBindingsAndEviction(t *testing.T) {
  current, lookups = nil, 0
  recordPCLocation(100, 7, "f", "a.go", 1)
  recordPCLocation(100, 8, "g", "b.go", 2)
  recordPCLocation(0, 8, "g", "b.go", 3)
  if lookups != 3 || len(current.frames) != 2 || current.frames[0].Entry != 8 || current.frames[0].Line != 2 {
    t.Fatal("PC and entry keys were conflated")
  }
  for i := 2; i < callerLocationLimit; i++ {
    current.frames = append(current.frames, CallerFrame{PC:uintptr(i+100), Entry:uintptr(i+1)})
  }
  oldestKept := current.frames[1]
  RecordPanicLocation(10000, "new", "new.go", 4)
  if len(current.frames) != callerLocationLimit || current.frames[0] != oldestKept ||
    current.frames[len(current.frames)-1].Function != "new" { t.Fatal("eviction order changed") }
  // A different logical goroutine owns an independent store.
  previous := current
  current = nil
  RecordCallerLocation(7, "second", "second.go", 5)
  if len(current.frames) != 1 || current == previous || previous.frames[0] != oldestKept {
    t.Fatal("cross-goroutine update leaked")
  }
}
`
