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
		"locationMatches": 1, "rememberLocation": 1,
	}
	var source bytes.Buffer
	source.WriteString("package caller\n")
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || functions[fn.Name.Name] == 0 {
			continue
		}
		if fn.Name.Name == "recordPCLocation" && fn.Recv != nil {
			// Count the production fallback's loop iterations, not wall time.
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				if loop, ok := node.(*ast.ForStmt); ok {
					loop.Body.List = append([]ast.Stmt{&ast.IncDecStmt{
						X: ast.NewIdent("linearLocationProbes"), Tok: token.INC,
					}}, loop.Body.List...)
				}
				return true
			})
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
	dir := t.TempDir()
	path := filepath.Join(dir, "caller.go")
	if err := os.WriteFile(path, source.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	testPath := filepath.Join(dir, "caller_test.go")
	if err := os.WriteFile(testPath, []byte(callerStoreUpdateSource), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-count=1", "-timeout=20s", "-cover", path, testPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual caller update helpers: %v\n%s", err, output)
	}
	t.Logf("extracted caller update helper coverage (not whole-runtime coverage):\n%s", output)
}

const callerStoreUpdateSource = `package caller
import "testing"
type CallerFrame struct {
  PC, Entry uintptr
  Function, File string
  Line, StartLine int
  captured uintptr
}
type callerLocationStore struct {
  frames, stack []CallerFrame
  lastLocation int
  locationHints [4]int
  nextLocationHint uint
}
const callerLocationLimit = 4096
var current *callerLocationStore
var lookups int
var linearLocationProbes int
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
func TestLocationHintValidity(t *testing.T) {
  for _, hint := range []int{-1, 0, 1, 2, 1000} {
    current = &callerLocationStore{lastLocation:hint, locationHints:[4]int{hint,-1,1000,hint}, frames:[]CallerFrame{
      {PC:0, Entry:7, Function:"entry"},
      {PC:7, Entry:8, Function:"pc"},
    }}
    recordPCLocation(0, 7, "entry updated", "a.go", 1)
    if current.lastLocation != 0 || current.frames[0].Function != "entry updated" || current.frames[1].Function != "pc" {
      t.Fatalf("hint %d confused PC and entry bindings: %+v", hint, current.frames)
    }
    for n := 0; n < 10; n++ {
      recordPCLocation(7, 99, "pc updated", "b.go", n+2)
      if current.lastLocation != 1 || current.frames[1].Entry != 99 || current.frames[1].Line != n+2 {
        t.Fatal("repeated cached PC binding did not update")
      }
    }
    // A slot can shift after eviction. A still-in-range stale hint must
    // compare the full key, rather than updating whichever record moved there.
    current.frames[0], current.frames[1] = current.frames[1], current.frames[0]
    recordPCLocation(7, 100, "moved", "c.go", 20)
    if current.lastLocation != 0 || current.frames[0].Function != "moved" || current.frames[1].Function != "entry updated" {
      t.Fatal("stale hint changed a different record")
    }
    current.frames = nil
    recordPCLocation(7, 101, "reset", "d.go", 21)
    if current.lastLocation != 0 || len(current.frames) != 1 || current.frames[0].Function != "reset" {
      t.Fatal("empty history reused a stale location")
    }
  }
}
func TestLocationHintAvoidsLinearSearch(t *testing.T) {
  current = &callerLocationStore{frames:make([]CallerFrame, callerLocationLimit)}
  for i := range current.frames { current.frames[i].Entry = uintptr(i+1) }
  linearLocationProbes = 0
  recordPCLocation(0, callerLocationLimit, "hot", "hot.go", 1)
  if linearLocationProbes != callerLocationLimit { t.Fatal("cold lookup did not exercise the full history") }
  linearLocationProbes = 0
  allocations := testing.AllocsPerRun(1000, func() {
    recordPCLocation(0, callerLocationLimit, "hot", "hot.go", 2)
  })
  if linearLocationProbes != 0 || allocations != 0 || len(current.frames) != callerLocationLimit ||
    current.frames[callerLocationLimit-1].Line != 2 {
    t.Fatalf("cached update: linear probes=%d allocations=%g", linearLocationProbes, allocations)
  }
}
func TestLocationHintsAlternatingCalls(t *testing.T) {
  current = &callerLocationStore{frames:make([]CallerFrame, callerLocationLimit)}
  for i := range current.frames { current.frames[i].Entry = uintptr(i+1) }
  keys := []struct { pc, entry uintptr }{{0,7001},{7001,9001},{0,7002},{7002,9002}}
  for i, key := range keys {
    current.frames[callerLocationLimit-len(keys)+i] = CallerFrame{PC:key.pc, Entry:key.entry}
    recordPCLocation(key.pc, key.entry, "hot", "hot.go", 1)
  }
  linearLocationProbes = 0
  allocations := testing.AllocsPerRun(1000, func() {
    for _, key := range keys { recordPCLocation(key.pc, key.entry, "hot", "hot.go", 2) }
  })
  if linearLocationProbes != 0 || allocations != 0 || len(current.frames) != callerLocationLimit {
    t.Fatalf("alternating updates: linear probes=%d allocations=%g", linearLocationProbes, allocations)
  }
  // Cached slots may move without changing the history length. Both PC and
  // entry-only keys must still resolve their actual record after the move.
  for i, key := range keys {
    slot := callerLocationLimit-len(keys)+i
    current.frames[i], current.frames[slot] = current.frames[slot], current.frames[i]
    recordPCLocation(key.pc, key.entry, "moved", "moved.go", 3)
    if current.frames[i].Function != "moved" || current.frames[i].Line != 3 || current.frames[slot].Function == "moved" {
      t.Fatal("a stale multi-location hint updated another record")
    }
  }
}
func TestLocationHintsMatchLinearHistory(t *testing.T) {
  current = &callerLocationStore{}
  var want []CallerFrame
  // Fill and repeatedly evict a full history while mixing new entries with
  // frequently rebound PC keys. Check against the original linear algorithm.
  for n := 1; n <= callerLocationLimit+1024; n++ {
    pc, entry := uintptr(0), uintptr(n)
    if n%7 == 0 { pc = uintptr(n%31+1) }
    recordPCLocation(pc, entry, "f", "a.go", n)
    i := 0
    for ; i < len(want); i++ {
      if (pc != 0 && want[i].PC == pc) || (pc == 0 && want[i].PC == 0 && want[i].Entry == entry) { break }
    }
    if i == len(want) {
      if len(want) == callerLocationLimit { want = want[1:]; i-- }
      want = append(want, CallerFrame{})
    }
    want[i] = CallerFrame{PC:pc, Entry:entry, Function:"f", File:"a.go", Line:n}
    if current.lastLocation != i || len(current.frames) != len(want) {
      t.Fatalf("history shape changed on update %d", n)
    }
    if n%17 == 0 || n == callerLocationLimit+1024 {
      for j := range want {
        if current.frames[j] != want[j] { t.Fatalf("history differs at update %d slot %d", n, j) }
      }
    }
  }
}
`
