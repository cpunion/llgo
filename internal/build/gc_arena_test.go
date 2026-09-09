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
	"runtime"
	"testing"
	"time"
)

// Exercise the production allocator, marker, sweeper, metadata, entry guard,
// statistics and pacer together, without using them to collect the test runner.
// Only platform memory/root discovery and finalizer/profiler hooks are shims.
// Finalizer and compiler-root integration remain separate target-side tests.
func TestGCIndependentArena(t *testing.T) {
	var source bytes.Buffer
	source.WriteString("package arena\nimport \"unsafe\"\n")
	dir := filepath.Join("..", "..", "runtime", "internal", "runtime", "tinygogc")
	for _, name := range []string{"gc_tinygo.go", "head_cache.go", "pacing.go", "gc.go", "mutex.go"} {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
				continue
			}
			if fn, ok := decl.(*ast.FuncDecl); ok && (fn.Name.Name == "gcPanic" || fn.Name.Name == "getsp" || fn.Name.Name == "gcReentryAbort") {
				continue
			}
			if err := format.Node(&source, fset, decl); err != nil {
				t.Fatal(err)
			}
			source.WriteByte('\n')
		}
	}
	generated := t.TempDir()
	collector := filepath.Join(generated, "collector.go")
	path := filepath.Join(generated, "arena_test.go")
	if err := os.WriteFile(collector, source.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(gcArenaTestSource), 0600); err != nil {
		t.Fatal(err)
	}
	files := []string{collector, path}
	for _, name := range []string{"pacing_test.go", "head_cache_test.go"} {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		file.Name.Name = "arena"
		var testSource bytes.Buffer
		if err := format.Node(&testSource, fset, file); err != nil {
			t.Fatal(err)
		}
		filePath := filepath.Join(generated, name)
		if err := os.WriteFile(filePath, testSource.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		files = append(files, filePath)
	}
	arches := []string{runtime.GOARCH}
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		arches = append(arches, "386")
	}
	for _, arch := range arches {
		t.Run(arch, func(t *testing.T) {
			ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 60*time.Second)
			defer cancel()
			args := append([]string{"test", "-v", "-count=1", "-timeout=45s", "-cpu=1,4", "-cover"}, files...)
			cmd := exec.CommandContext(ctx, "go", args...)
			cmd.Env = append(os.Environ(), "GOARCH="+arch, "CGO_ENABLED=0")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("independent collector arena (%s): %v\n%s", arch, err, out)
			}
			t.Logf("single-mutator arena on %s/%s (NOT concurrent GC):\n%s", runtime.GOOS, arch, out)
		})
	}
}

const gcArenaTestSource = `package arena
import (
  "strings"
  "sync"
  "testing"
  "unsafe"
)

var backing []byte
var arenaBase, arenaInitial, arenaMaximum uintptr
var arenaGrow bool
var arenaRoots []unsafe.Pointer
var arenaPacing gcPacing
var memoryHook func()
var profileFrees []uintptr

func newArena(initial, maximum uintptr, grow bool) {
  // Canaries are outside the maximum heap, not inside its metadata.
  backing = make([]byte, maximum+4*bytesPerBlock)
  for i := range backing { backing[i] = 0xa5 }
  arenaBase = (uintptr(unsafe.Pointer(&backing[0]))+2*bytesPerBlock-1)&^(bytesPerBlock-1)
  arenaInitial, arenaMaximum, arenaGrow = initial, maximum, grow
  for i := uintptr(0); i < maximum; i++ { *(*byte)(unsafe.Pointer(arenaBase+i)) = 0 }
  heapStart, heapEnd, globalsStart, globalsEnd, stackTop, endBlock = 0,0,0,0,0,0
  metadataStart = nil
  nextAlloc, gcTotalAlloc, gcTotalBlocks, gcMallocs, gcFrees, gcFreedBlocks, gcNumGC = 0,0,0,0,0,0,0
  markStackOverflow, isGCInit = false, false
  markHeads, gcMutex, arenaPacing = markHeadCache{}, mutex{}, gcPacing{}
  arenaRoots, profileFrees, memoryHook = nil, nil, nil
}
func checkCanaries(t *testing.T) {
  t.Helper()
  base := uintptr(unsafe.Pointer(&backing[0]))
  for i, value := range backing {
    address := base+uintptr(i)
    if (address < arenaBase || address >= arenaBase+arenaMaximum) && value != 0xa5 {
      t.Fatalf("arena boundary overwritten at byte %d: %x", i, value)
    }
  }
}
func memory(p unsafe.Pointer, n uintptr) []byte {
  address := uintptr(p)
  if address < arenaBase || address > arenaBase+arenaMaximum || n > arenaBase+arenaMaximum-address {
    panic("arena memory operation out of bounds")
  }
  return unsafe.Slice((*byte)(p), n)
}
var c = struct {
  Str func(string) string
  Memset func(unsafe.Pointer, int, uintptr) unsafe.Pointer
  Memcpy func(unsafe.Pointer, unsafe.Pointer, uintptr) unsafe.Pointer
  Memmove func(unsafe.Pointer, unsafe.Pointer, uintptr) unsafe.Pointer
}{
  func(s string) string { return s },
  func(p unsafe.Pointer, v int, n uintptr) unsafe.Pointer {
    if memoryHook != nil { hook := memoryHook; memoryHook = nil; hook() }
    b := memory(p,n); for i := range b { b[i] = byte(v) }; return p
  },
  func(dst, src unsafe.Pointer, n uintptr) unsafe.Pointer { copy(memory(dst,n),memory(src,n)); return dst },
  func(dst, src unsafe.Pointer, n uintptr) unsafe.Pointer { copy(memory(dst,n),memory(src,n)); return dst },
}
func gcPanic(s string) { panic(s) }
func gcReentryAbort() { panic("gc: reentrant entry rejected") }
func gcMemoryLayout() (uintptr,uintptr,uintptr,uintptr,uintptr) { return arenaBase,arenaBase+arenaInitial,0,0,0 }
func gcGrowMemory(old uintptr) uintptr {
  if !arenaGrow || old == arenaBase+arenaMaximum { return old }
  size := 2*(old-arenaBase)
  if size > arenaMaximum { size = arenaMaximum }
  return arenaBase+size
}
func gcMarkReachable() { for _, p := range arenaRoots { markRoot(0,uintptr(p)) } }
func gcStackStats() (uintptr,uintptr) { return 0,0 }
func gcAutomaticAllowed() bool { return !arenaGrow || arenaPacing.automatic() }
func gcAllocationDue(n uint64) bool { return arenaGrow && arenaPacing.shouldCollect(gcLiveBytes(),n) }
func gcCollectionComplete() { if arenaGrow { arenaPacing.collected(gcLiveBytes()) } }
func gcNextGoal() uint64 { if arenaGrow { return arenaPacing.nextGC() }; return 0 }
func gcRootAllocated(n uint64) { if arenaGrow { arenaPacing.rootAllocated(n) } }
func gcRootFreed(n uint64) { if arenaGrow { arenaPacing.rootFreed(n) } }
func memProfileFree(address uintptr) { profileFrees = append(profileFrees,address) }
func noteFinalizerReference(block uintptr) {}
func preserveFinalizableObjects() {}
func scheduleFinalizers() {}
func wantPanic(t *testing.T, text string, fn func()) {
  t.Helper()
  defer func() {
    got := recover()
    message, ok := got.(string)
    if !ok || !strings.Contains(message,text) { t.Fatalf("panic=%v, want %q",got,text) }
  }()
  fn()
}

func TestAllocationAndRelease(t *testing.T) {
  for _, grow := range []bool{false,true} {
    newArena(64<<10,256<<10,grow)
    if Alloc(0) == nil || AllocRoot(0) != Alloc(0) { t.Fatal("zero-sized allocation contract") }
    FreeRoot(AllocRoot(0)); Free(nil)
    for _, size := range []uintptr{1,bytesPerBlock-1,bytesPerBlock,bytesPerBlock+1,1025} {
      p := AllocRoot(size)
      if uintptr(p)%bytesPerBlock != 0 { t.Fatal("misaligned payload") }
      for _, b := range memory(p,size) { if b != 0 { t.Fatal("new allocation was not zeroed") } }
      for i := range memory(p,size) { memory(p,size)[i] = 0x39 }
      before := ReadGCStats()
      if before.HeapAlloc != uint64(AllocationSize(size)) { t.Fatalf("rounded allocation: %+v",before) }
      FreeRoot(p)
      after := ReadGCStats()
      if after.HeapAlloc != 0 || after.Frees != before.Frees+1 { t.Fatalf("explicit root release: %+v",after) }
      FreeRoot(p)
      if ReadGCStats().Frees != after.Frees { t.Fatal("unreused double free was charged twice") }
      for _, b := range memory(p,AllocationSize(size)) { if b != 0 { t.Fatal("released payload not cleared") } }
    }
    checkCanaries(t)
  }
}

func TestGraphMarkSweepAndOverflow(t *testing.T) {
  for _, grow := range []bool{false,true} {
    newArena(1<<20,1<<20,grow)
    count := int(markStackSize)+17
    root := Alloc(uintptr(count)*unsafe.Sizeof(uintptr(0)))
    children, leaves := make([]unsafe.Pointer,count),make([]unsafe.Pointer,count)
    slots := unsafe.Slice((*unsafe.Pointer)(root),count)
    for i := range children {
      children[i], leaves[i] = Alloc(2*unsafe.Sizeof(uintptr(0))), Alloc(bytesPerBlock+1)
      slots[i] = children[i]
      // Interior pointers and cycles must preserve exactly the same graph.
      child := unsafe.Slice((*unsafe.Pointer)(children[i]),2)
      child[0], child[1] = unsafe.Add(leaves[i],1),root
    }
    dead := Alloc(97)
    startMark(blockFromAddr(uintptr(root)))
    if !markStackOverflow { t.Fatal("graph did not exercise the worklist overflow path") }
    finishMark()
    if markStackOverflow { t.Fatal("overflow not drained") }
    for _, p := range append(children,leaves...) {
      if gcStateOf(blockFromAddr(uintptr(p))) != blockStateMark { t.Fatal("reachable descendant lost") }
    }
    sweep()
    if gcStateOf(blockFromAddr(uintptr(dead))) != blockStateFree { t.Fatal("dead object survived") }
    arenaRoots = []unsafe.Pointer{unsafe.Add(root,1)}
    before := ReadGCStats()
    GC()
    after := ReadGCStats()
    if after.HeapAlloc != before.HeapAlloc || after.NumGC != before.NumGC+1 { t.Fatal("interior root lost graph") }
    arenaRoots = nil
    GC()
    if ReadGCStats().HeapAlloc != 0 { t.Fatal("unrooted graph was not reclaimed") }
    checkCanaries(t)
  }
}

func TestFixedArenaExhaustionAndGrowingArena(t *testing.T) {
  for _, grow := range []bool{false,true} {
    newArena(16<<10,64<<10,grow)
    p := AllocRoot(12<<10)
    arenaRoots = []unsafe.Pointer{p}
    memory(p,12<<10)[0] = 0x7b
    arenaPacing.init(-1,gcLiveBytes())
    if !grow {
      wantPanic(t,"out of memory",func(){ Alloc(24<<10) })
      if heapEnd != arenaBase+arenaInitial || gcNumGC != 1 { t.Fatal("fixed-heap capacity policy changed") }
    } else {
      q := AllocRoot(24<<10)
      arenaRoots = append(arenaRoots,q)
      if heapEnd <= arenaBase+arenaInitial || gcNumGC != 0 { t.Fatal("disabled GC did not grow without collecting") }
      if memory(p,12<<10)[0] != 0x7b { t.Fatal("growth damaged old allocation") }
      GC()
      if gcNumGC != 1 || gcNextGoal() != disabledGCGoal { t.Fatal("explicit GC changed disabled policy") }
      wantPanic(t,"out of memory",func(){ Alloc(64<<10) })
    }
    checkCanaries(t)
  }
}

func TestRootPacingAndOrdinaryPressure(t *testing.T) {
  newArena(1<<20,2<<20,true)
  warm := AllocRoot(128<<10)
  arenaRoots = []unsafe.Pointer{warm}
  arenaPacing.init(0,gcLiveBytes())
  before := ReadGCStats()
  p := AllocRoot(128<<10)
  arenaRoots = append(arenaRoots,p)
  after := ReadGCStats()
  if after.NumGC != before.NumGC || after.NextGC-after.HeapAlloc != before.NextGC-before.HeapAlloc {
    t.Fatal("root allocation consumed the Go-object budget")
  }
  q := Alloc(uintptr(after.NextGC-after.HeapAlloc))
  arenaRoots = append(arenaRoots,q)
  if gcNumGC != before.NumGC+1 { t.Fatal("ordinary heap pressure did not collect") }
  goal := gcNextGoal()
  FreeRoot(p)
  if gcNextGoal() != goal-uint64(AllocationSize(128<<10)) { t.Fatal("root release did not retire its budget credit") }
  arenaRoots = []unsafe.Pointer{warm,q}
  GC()
  checkCanaries(t)
}

func TestReallocation(t *testing.T) {
  newArena(64<<10,128<<10,true)
  p := Realloc(nil,37)
  arenaRoots = []unsafe.Pointer{p}
  for i := range memory(p,37) { memory(p,37)[i] = byte(i+1) }
  if Realloc(p,12) != p { t.Fatal("shrinking realloc changed storage") }
  q := Realloc(p,1027)
  for i,b := range memory(q,37) { if b != byte(i+1) { t.Fatal("growing realloc lost payload") } }
  if gcStateOf(blockFromAddr(uintptr(p))) != blockStateFree { t.Fatal("old realloc storage not released") }
  checkCanaries(t)
}

func TestReentrantEntryRejected(t *testing.T) {
  for _, enter := range []func(){func(){Alloc(1)},func(){GC()},func(){ReadGCStats()}} {
    newArena(64<<10,64<<10,false)
    Alloc(16) // Complete bootstrap before injecting the host callback.
    before := gcMallocs
    memoryHook = enter
    wantPanic(t,"reentrant entry rejected",func(){Alloc(32)})
    if gcMallocs != before { t.Fatal("nested entry committed an allocation") }
    checkCanaries(t)
  }
  // A callback after the outer operation has released ownership is legal.
  newArena(64<<10,64<<10,false)
  p := Alloc(8); arenaRoots = []unsafe.Pointer{p}; GC(); Alloc(8)
  if gcMutex.active { t.Fatal("completed operation retained entry guard") }
}

func TestSerializedRequestsOnMulticoreHost(t *testing.T) {
  // This checks serialized callers on GOMAXPROCS=1 and 4, not multiple
  // concurrent mutators. ALL root/payload access stays under one owner.
  newArena(64<<10,64<<10,false)
  var owner sync.Mutex
  var wg sync.WaitGroup
  for worker := 0; worker < 8; worker++ {
    wg.Add(1)
    go func(){
      defer wg.Done()
      for i := 0; i < 40; i++ {
        owner.Lock()
        p := AllocRoot(64)
        arenaRoots = []unsafe.Pointer{p}
        *(*uintptr)(p) = 17
        GC()
        if *(*uintptr)(p) != 17 { t.Error("serialized caller lost root") }
        arenaRoots = nil
        FreeRoot(p)
        owner.Unlock()
      }
    }()
  }
  wg.Wait()
  if ReadGCStats().HeapAlloc != 0 || gcMutex.active { t.Fatal("serialized requests leaked ownership/storage") }
  checkCanaries(t)
}
`
