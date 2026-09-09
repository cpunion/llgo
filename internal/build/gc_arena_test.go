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

// The embedded serial fixture calls ReadGCStats through go:linkname. Its
// hand-written result must match the complete value-return ABI, not a prefix.
func TestEmbeddedGCStatsABI(t *testing.T) {
	fields := func(path, name string) string {
		t.Helper()
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typ := spec.(*ast.TypeSpec)
				if typ.Name.Name == name {
					var out bytes.Buffer
					if err := format.Node(&out, fset, typ.Type); err != nil {
						t.Fatal(err)
					}
					return string(bytes.Join(bytes.Fields(out.Bytes()), []byte(" ")))
				}
			}
		}
		t.Fatalf("missing %s in %s", name, path)
		return ""
	}
	root := filepath.Join("..", "..")
	want := fields(filepath.Join(root, "runtime/internal/runtime/tinygogc/gc.go"), "GCStats")
	got := fields(filepath.Join(root, "_demo/embed/testdata/esp32-serial/gc-runtime/main.go"), "gcStats")
	if got != want {
		t.Fatalf("embedded GC result ABI differs from collector\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// Exercise the production allocator, marker, sweeper, metadata, entry guard,
// statistics and pacer together, without using them to collect the test runner.
// Only platform memory/root discovery and finalizer/profiler hooks are shims.
// Finalizer and compiler-root integration remain separate target-side tests.
func TestGCIndependentArena(t *testing.T) {
	testGCIndependentArena(t, false)
}

// The lifecycle variant also executes the real callback registry and dependency
// traversal. Its worker is drained explicitly, so the host runner never becomes
// a second mutator of the arena.
func TestGCFinalizerArena(t *testing.T) {
	testGCIndependentArena(t, true)
}

const gcFinalizerArenaTestSource = `
var arenaFinalizerSteps uint64
var arenaIndexAllocationHook func()
func resetArenaFinalizers() {
  finalizers, readyFinalizers = nil, nil
  finalizerIndex, finalizerCount = nil, 0
  finalizerWorkerRunning, finalizerDependencyScan = false, false
  arenaFinalizerSteps = 0
  arenaIndexAllocationHook = nil
}
func arenaMakeFinalizerIndex(size int) []*finalizerRecord {
  if gcMutex.active { panic("finalizer index allocation under collector guard") }
  if hook := arenaIndexAllocationHook; hook != nil {
    arenaIndexAllocationHook = nil
    hook()
  }
  return make([]*finalizerRecord,size)
}
func markArenaFinalizerRoots() {
  // Registry records/callback closures live in the host Go heap; only their
  // published ready arguments are roots in this collector's test arena.
  for r := readyFinalizers; r != nil; r = r.readyNext {
    if r.ready != nil { markRoot(0, uintptr(r.ready)) }
  }
}
func readyCount() int {
  n := 0
  for r := readyFinalizers; r != nil; r = r.readyNext { n++ }
  return n
}
func checkFinalizerIndex(t *testing.T) {
  t.Helper()
  seen:=map[*finalizerRecord]bool{}
  var prev *finalizerRecord
  for r:=finalizers;r!=nil;r=r.next {
    if seen[r] || r.prev!=prev {t.Fatal("registry cycle or broken predecessor")}
    seen[r]=true;prev=r
  }
  if len(seen)!=finalizerCount {t.Fatal("registry count mismatch")}
  for bucket,r:=range finalizerIndex {
    for ;r!=nil;r=r.indexNext {
      if !seen[r] || finalizerBucket(r.objectKey,len(finalizerIndex))!=uintptr(bucket) {t.Fatal("stale/duplicate/misplaced index entry")}
      delete(seen,r)
    }
  }
  if len(seen)!=0 {t.Fatal("unindexed registry entry")}
}
func TestFinalizerRegistryScaling(t *testing.T) {
  for _, count := range []int{64, 1024, 2048} {
    newArena(512<<10,512<<10,false)
    called := 0
    for i := 0; i < count; i++ {
      p := Alloc(2*bytesPerBlock)
      *(*uintptr)(p) = 123
      _, ok := AddFinalizer(p, func(p unsafe.Pointer) {
        if *(*uintptr)(p) != 123 { t.Fatal("finalizer argument was not preserved") }
        called++
      })
      if !ok { t.Fatal("registration failed") }
    }
    checkFinalizerIndex(t)
    arenaFinalizerSteps = 0
    GC()
    steps := arenaFinalizerSteps
    if readyCount() != count || finalizers != nil { t.Fatal("lost or retained callbacks") }
    checkFinalizerIndex(t)
    // Recollection before dispatch must retain the queued objects.
    GC()
    drainFinalizers()
    if called != count || readyFinalizers != nil { t.Fatal("callback count") }
    GC()
    if ReadGCStats().HeapAlloc != 0 { t.Fatal("finalized objects were not reclaimed") }
    t.Logf("callbacks=%d registry iterations=%d", count, steps)
    if steps > uint64(count)*128 { t.Fatalf("quadratic callback scan: %d iterations for %d callbacks", steps, count) }
    checkCanaries(t)
  }
}
func TestFinalizerIndexCollectionDuringGrowth(t *testing.T) {
  newArena(64<<10,64<<10,false)
  called:=0
  for i:=0;i<16;i++ {AddFinalizer(Alloc(64),func(unsafe.Pointer){called++})}
  p:=Alloc(64)
  arenaRoots=[]unsafe.Pointer{p} // the registering caller's live argument
  arenaIndexAllocationHook=func(){GC();drainFinalizers()}
  AddFinalizer(p,func(unsafe.Pointer){called++})
  if arenaIndexAllocationHook!=nil || called!=16 || finalizerCount!=1 {t.Fatal("growth did not tolerate collection of the old registry")}
  checkFinalizerIndex(t)
  arenaRoots=nil
  GC();drainFinalizers();GC()
  if called!=17 || ReadGCStats().HeapAlloc!=0 {t.Fatal("registration was lost during growth")}
  checkFinalizerIndex(t)
}
func TestFinalizerIndexCollisions(t *testing.T) {
  newArena(64<<10,64<<10,false)
  var cancels []func()
  called:=0
  // Same low bucket bits, distinct object keys. The index must still compare
  // the full encoded address, including after unlinking a middle bucket node.
  for i:=0;i<12;i++ {
    cancel,_:=AddFinalizer(Alloc(16*bytesPerBlock),func(unsafe.Pointer){called++})
    cancels=append(cancels,cancel)
  }
  checkFinalizerIndex(t)
  for i:=0;i<12;i+=2 {cancels[i]();checkFinalizerIndex(t)}
  GC();checkFinalizerIndex(t);drainFinalizers();GC()
  if called!=6 || ReadGCStats().HeapAlloc!=0 {t.Fatal("colliding callback lost or canceled callback dispatched")}
}
func TestFinalizerDependencies(t *testing.T) {
  newArena(64<<10,64<<10,false)
  a,b,c,middle := Alloc(64),Alloc(64),Alloc(64),Alloc(64)
  *(*unsafe.Pointer)(a) = middle
  *(*unsafe.Pointer)(middle) = b
  *(*unsafe.Pointer)(b) = c
  var events []int
  for i,p := range []unsafe.Pointer{c,a,b} {
    id := []int{3,1,2}[i]
    AddFinalizer(p,func(unsafe.Pointer){events=append(events,id)})
  }
  for want := 1; want <= 3; want++ {
    GC()
    if readyCount()!=1 { t.Fatalf("dependency stage %d: ready=%d",want,readyCount()) }
    drainFinalizers()
    if len(events)!=want || events[want-1]!=want { t.Fatalf("dependency order: %v",events) }
  }
  GC()
  if ReadGCStats().HeapAlloc!=0 { t.Fatal("dependency graph retained after callbacks") }
}
func TestFinalizerCyclesAndLiveRoots(t *testing.T) {
  newArena(64<<10,64<<10,false)
  a,b := Alloc(64),Alloc(64)
  *(*unsafe.Pointer)(a)=b
  *(*unsafe.Pointer)(b)=a
  AddFinalizer(a,func(unsafe.Pointer){t.Fatal("cycle finalized")})
  AddFinalizer(b,func(unsafe.Pointer){t.Fatal("cycle finalized")})
  for i:=0;i<3;i++ {GC();if readyCount()!=0 {t.Fatal("cycle queued")}}
  if gcStateOf(blockFromAddr(uintptr(a)))!=blockStateHead || gcStateOf(blockFromAddr(uintptr(b)))!=blockStateHead {t.Fatal("cycle collected")}
  newArena(64<<10,64<<10,false)
  p:=Alloc(64)
  arenaRoots=[]unsafe.Pointer{unsafe.Add(p,8)}
  AddFinalizer(p,func(unsafe.Pointer){})
  GC()
  if readyCount()!=0 {t.Fatal("live object queued")}
  arenaRoots=nil
  GC()
  if readyCount()!=1 {t.Fatal("dropped root not queued")}
  drainFinalizers()
}
func TestFinalizerCleanupAndCancellation(t *testing.T) {
  newArena(64<<10,64<<10,false)
  p,q := Alloc(64),Alloc(64)
  var finalCalls,cleanupCalls int
  AddCleanup(p,func(unsafe.Pointer){cleanupCalls++})
  cancelQ,_:=AddFinalizer(q,func(unsafe.Pointer){t.Fatal("canceled active callback ran")})
  AddFinalizer(unsafe.Add(p,8),func(got unsafe.Pointer){
    if got!=unsafe.Add(p,8) {t.Fatal("interior pointer changed")}
    finalCalls++
    // User callbacks run after the collection guard is released.
    Alloc(64)
  })
  AddCleanup(unsafe.Add(p,16),func(unsafe.Pointer){cleanupCalls++})
  AddFinalizer(p,func(unsafe.Pointer){finalCalls++})
  cancelQ();cancelQ()
  GC()
  if readyCount()!=2 {t.Fatalf("expected only the two finalizers, got %d",readyCount())}
  drainFinalizers()
  if finalCalls!=2 || cleanupCalls!=0 {t.Fatal("cleanup ran before finalizers")}
  GC();drainFinalizers()
  if cleanupCalls!=2 {t.Fatal("lost cleanup")}
  p=Alloc(64)
  cancel,_:=AddCleanup(p,func(unsafe.Pointer){t.Fatal("canceled queued cleanup ran")})
  GC();cancel();cancel();drainFinalizers()
  if readyFinalizers!=nil || finalizerWorkerRunning {t.Fatal("cancellation left worker active")}
  GC()
  if ReadGCStats().HeapAlloc!=0 {t.Fatal("cancellation leaked storage")}
}
func TestFinalizerInvalidRegistration(t *testing.T) {
  newArena(64<<10,64<<10,false)
  p:=Alloc(64)
  cases:=[]struct{p unsafe.Pointer; callback func(unsafe.Pointer)}{
    {nil,func(unsafe.Pointer){}},{p,nil},{unsafe.Pointer(&arenaFinalizerSteps),func(unsafe.Pointer){}},
  }
  for _,c:=range cases {
    cancel,ok:=AddFinalizer(c.p,c.callback)
    if ok {t.Fatal("invalid registration accepted")}
    cancel()
  }
  Free(p)
  cancel,ok:=AddFinalizer(p,func(unsafe.Pointer){})
  if ok {t.Fatal("free block registered")};cancel()
  if finalizers!=nil {t.Fatal("invalid registration retained")}
}
`

func testGCIndependentArena(t *testing.T, lifecycle bool) {
	var source bytes.Buffer
	source.WriteString("package arena\nimport \"unsafe\"\n")
	dir := filepath.Join("..", "..", "runtime", "internal", "runtime", "tinygogc")
	names := []string{"gc_tinygo.go", "head_cache.go", "pacing.go", "gc.go", "mutex.go", "noscan_wasm.go"}
	if lifecycle {
		names = append(names, "finalizer.go")
	}
	for _, name := range names {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
				continue
			}
			if fn, ok := decl.(*ast.FuncDecl); ok && (fn.Name.Name == "gcPanic" || fn.Name.Name == "getsp" || fn.Name.Name == "gcReentryAbort" || fn.Name.Name == "scheduleFinalizers") {
				continue
			}
			if name == "finalizer.go" {
				// Count actual registry iterations, not elapsed time. Do not
				// replace the search/queue implementation with a test model.
				ast.Inspect(decl, func(node ast.Node) bool {
					if call, ok := node.(*ast.CallExpr); ok {
						if name, ok := call.Fun.(*ast.Ident); ok && name.Name == "make" {
							// The one index allocation may collect before publication.
							// Exercise that window without allocating the index in
							// the test arena or changing the production control flow.
							call.Fun = ast.NewIdent("arenaMakeFinalizerIndex")
							call.Args = call.Args[1:]
						}
					}
					var body *ast.BlockStmt
					switch loop := node.(type) {
					case *ast.ForStmt:
						body = loop.Body
					case *ast.RangeStmt:
						body = loop.Body
					}
					if body != nil {
						body.List = append([]ast.Stmt{&ast.IncDecStmt{X: ast.NewIdent("arenaFinalizerSteps"), Tok: token.INC}}, body.List...)
					}
					return true
				})
			}
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "gcStateByteOf" {
				// Count metadata probes without replacing the production search.
				// This makes the large occupied-run regression deterministic,
				// independent of host clock speed or CI contention.
				fn.Body.List = append([]ast.Stmt{&ast.IncDecStmt{
					X: ast.NewIdent("arenaMetadataReads"), Tok: token.INC,
				}}, fn.Body.List...)
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
	shim := []byte(gcArenaTestSource)
	if lifecycle {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "arena_test.go", shim, 0)
		if err != nil {
			t.Fatal(err)
		}
		decls := file.Decls[:0]
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && (fn.Name.Name == "noteFinalizerReference" || fn.Name.Name == "preserveFinalizableObjects" || fn.Name.Name == "resetArenaFinalizers" || fn.Name.Name == "markArenaFinalizerRoots") {
				continue
			}
			decls = append(decls, decl)
		}
		file.Decls = decls
		var out bytes.Buffer
		if err := format.Node(&out, fset, file); err != nil {
			t.Fatal(err)
		}
		out.WriteString(gcFinalizerArenaTestSource)
		shim = out.Bytes()
	}
	if err := os.WriteFile(path, shim, 0600); err != nil {
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
var arenaMetadataReads uint64

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
  noScanIndex, noScanCount = nil, 0
  arenaRoots, profileFrees, memoryHook = nil, nil, nil
  arenaMetadataReads = 0
  resetArenaFinalizers()
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
func gcMarkReachable() {
  for _, p := range arenaRoots { markRoot(0,uintptr(p)) }
  markArenaFinalizerRoots()
}
func resetArenaFinalizers() {}
func markArenaFinalizerRoots() {}
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

func TestNoScanRootDoesNotTracePayload(t *testing.T) {
  newArena(64<<10,64<<10,false)
  normal, normalChild := AllocRoot(64), Alloc(64)
  *(*unsafe.Pointer)(normal) = normalChild
  arenaRoots = []unsafe.Pointer{normal}
  GC()
  if gcStateOf(blockFromAddr(uintptr(normalChild))) != blockStateHead {
    t.Fatal("ordinary root did not trace its payload")
  }
  arenaRoots = nil
  FreeRoot(normal)
  GC()
  if gcStateOf(blockFromAddr(uintptr(normalChild))) != blockStateFree {
    t.Fatal("ordinary child remained live after dropping its root")
  }

  // Reach the no-scan allocation through another heap object. The allocation
  // itself remains live, but its stale payload must not retain a child.
  holder, opaque, stale := Alloc(64), AllocNoScanRoot(64), Alloc(64)
  *(*unsafe.Pointer)(holder) = opaque
  *(*unsafe.Pointer)(opaque) = stale
  arenaRoots = []unsafe.Pointer{holder}
  GC()
  if gcStateOf(blockFromAddr(uintptr(holder))) != blockStateHead ||
      gcStateOf(blockFromAddr(uintptr(opaque))) != blockStateHead {
    t.Fatal("indirect no-scan allocation was not retained")
  }
  if gcStateOf(blockFromAddr(uintptr(stale))) != blockStateFree {
    t.Fatal("no-scan payload retained a stale child")
  }
  arenaRoots = nil
  FreeNoScanRoot(opaque)
  GC()
  if ReadGCStats().HeapAlloc != 0 || noScanCount != 0 {
    t.Fatal("no-scan release leaked storage or registry state")
  }
  checkCanaries(t)
}

func TestNoScanRootIndexGrowthAndRelease(t *testing.T) {
  newArena(256<<10,256<<10,false)
  roots := make([]unsafe.Pointer, 65)
  for i := range roots {
    roots[i] = AllocNoScanRoot(64)
    *(*unsafe.Pointer)(roots[i]) = Alloc(64)
  }
  arenaRoots = roots
  GC()
  if noScanCount != len(roots) || len(noScanIndex) < len(roots) {
    t.Fatal("no-scan index did not grow with its registrations")
  }
  for _, root := range roots {
    if gcStateOf(blockFromAddr(uintptr(root))) != blockStateHead {
      t.Fatal("indexed no-scan allocation was not retained")
    }
    if stale := *(*unsafe.Pointer)(root); gcStateOf(blockFromAddr(uintptr(stale))) != blockStateFree {
      t.Fatal("indexed no-scan allocation traced its payload")
    }
  }
  arenaRoots = nil
  for _, root := range roots { FreeNoScanRoot(root) }
  GC()
  if ReadGCStats().HeapAlloc != 0 || noScanCount != 0 {
    t.Fatal("growing no-scan index leaked storage or registrations")
  }
  checkCanaries(t)
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

func TestAllocationSkipsOccupiedRun(t *testing.T) {
  newArena(1<<20,1<<20,false)
  hole := Alloc(1)
  large := Alloc(256<<10)
  arenaRoots = []unsafe.Pointer{large}
  Free(hole)
  arenaMetadataReads = 0
  p := Alloc(2*bytesPerBlock)
  probes := arenaMetadataReads
  if uintptr(p) != uintptr(large)+AllocationSize(256<<10) {
    t.Fatal("occupied-run skip changed first-fit allocation order")
  }
  if probes >= uint64((256<<10)/bytesPerBlock)/4 {
    t.Fatalf("allocation probed %d metadata bytes while skipping one large object",probes)
  }
  checkCanaries(t)
}

func TestAllocationOccupiedRunCannotSkipSearchOrigin(t *testing.T) {
  newArena(64<<10,64<<10,false)
  hole := Alloc(1)
  large := Alloc((endBlock-1)*bytesPerBlock)
  arenaRoots = []unsafe.Pointer{large}
  Free(hole)
  // The search origin is allowed to be any block. On its second traversal,
  // a bulk occupied-run skip must stop here so exhaustion still collects
  // once and reports OOM, rather than looping forever around the origin.
  nextAlloc = blockFromAddr(uintptr(large))+2
  wantPanic(t,"out of memory",func(){Alloc(2*bytesPerBlock)})
  if gcNumGC != 1 { t.Fatalf("exhausted search collected %d times, want 1",gcNumGC) }
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
