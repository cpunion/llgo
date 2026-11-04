package runtime

import (
	"bytes"
	"context"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	stdruntime "runtime"
	"testing"
	"time"
)

func TestPrebuiltFuncCacheLazyPublicationAndRetention(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "internal/lib/runtime/symtab.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{
		"runtimePrebuiltFtabEntry": true, "runtimePrebuiltFtab": true,
		"runtimePrebuiltFuncs":  true,
		"prebuiltFuncCacheLoad": true, "prebuiltFuncCacheStore": true,
	}
	var source bytes.Buffer
	source.WriteString("package runtime\nimport(latomic \"sync/atomic\"; \"unsafe\")\n")
	for _, declaration := range file.Decls {
		name := ""
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			name = declaration.Name.Name
		case *ast.GenDecl:
			if len(declaration.Specs) == 1 {
				switch spec := declaration.Specs[0].(type) {
				case *ast.TypeSpec:
					name = spec.Name.Name
				case *ast.ValueSpec:
					if len(spec.Names) == 1 {
						name = spec.Names[0].Name
					}
				}
			}
		}
		if wanted[name] {
			if err := format.Node(&source, fset, declaration); err != nil {
				t.Fatal(err)
			}
			source.WriteByte('\n')
			delete(wanted, name)
		}
	}
	if len(wanted) != 0 {
		t.Fatal("missing production cache declarations", wanted)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"go.mod":        []byte("module example.com/prebuilt-cache-contract\n\ngo 1.27.0\n"),
		"cache.go":      source.Bytes(),
		"cache_test.go": []byte(prebuiltFuncCacheHostContract),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Each process must begin with a cold global pointer. The projected helper
	// bodies are the production declarations; native metadata tests cover their
	// integration with the actual initializer and conservative collector.
	for repeat := 0; repeat < 3; repeat++ {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		cmd := exec.CommandContext(ctx, filepath.Join(stdruntime.GOROOT(), "bin/go"), "test", "-p=1", "-race", "-count=1", "-timeout=70s", "-v", ".")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOMAXPROCS=2")
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("cache projection round %d: %v\n%s", repeat, err, output)
		}
		t.Log(string(output))
	}
}

const prebuiltFuncCacheHostContract = `package runtime

import (
 stdruntime "runtime"
 "sync"
 "sync/atomic"
 "testing"
 "unsafe"
)

type rowCacheProbe struct {
 sequence uint64
 payload [4096]byte
}

func TestColdRowsConcurrentPublicationAndTypedGC(t *testing.T) {
 const count = 8193
 const workers = 32
 if runtimePrebuiltFuncs != nil { t.Fatal("cache is not initially cold") }
 sentinel := &rowCacheProbe{sequence: 99}
 // An absent metadata table must never allocate or index candidate[0].
 if n := testing.AllocsPerRun(100, func() {
  for _, index := range []int{-1, 0, int(^uint(0)>>1)} {
   if prebuiltFuncCacheLoad(index) != nil { panic("empty table read returned a value") }
   prebuiltFuncCacheStore(index, unsafe.Pointer(sentinel))
  }
 }); n != 0 || runtimePrebuiltFuncs != nil {
  t.Fatal("empty table allocated a cache", n)
 }
 runtimePrebuiltFtab = make([]runtimePrebuiltFtabEntry, count)
 if n := testing.AllocsPerRun(100, func() {
  for _, index := range []int{-1, 0, count-1, count, int(^uint(0)>>1)} {
   if prebuiltFuncCacheLoad(index) != nil { panic("cold read returned a value") }
   prebuiltFuncCacheStore(index, nil)
  }
  prebuiltFuncCacheStore(-1, unsafe.Pointer(sentinel))
  prebuiltFuncCacheStore(count, unsafe.Pointer(sentinel))
 }); n != 0 || runtimePrebuiltFuncs != nil {
  t.Fatal("cold misses, nil stores or invalid indices allocated", n)
 }
 var done sync.WaitGroup
 var addresses [workers]uintptr
 start := make(chan struct{})
 done.Add(workers)
 for worker := range workers {
  go func(worker int) {
   defer done.Done()
   <-start
   probe := &rowCacheProbe{sequence: uint64(worker+1)}
   probe.payload[0] = byte(worker)
   probe.payload[len(probe.payload)-1] = ^byte(worker)
   prebuiltFuncCacheStore(worker*(count-1)/(workers-1), unsafe.Pointer(probe))
   addresses[worker] = uintptr(atomic.LoadPointer(&runtimePrebuiltFuncs))
  }(worker)
 }
 close(start)
 for range 3 { stdruntime.GC() }
 done.Wait()
 for _, address := range addresses {
  if address == 0 || address != addresses[0] { t.Fatal("multiple published tables") }
 }
 // Only the production global retains the pointer-bearing cache and probes.
 for range 3 {
  churn := make([][]byte, 128)
  for i := range churn { churn[i] = make([]byte, 16384) }
  stdruntime.GC()
  stdruntime.KeepAlive(churn)
 }
 for worker := range workers {
  index := worker*(count-1)/(workers-1)
  probe := (*rowCacheProbe)(prebuiltFuncCacheLoad(index))
  if probe == nil || probe.sequence != uint64(worker+1) || probe.payload[0] != byte(worker) || probe.payload[len(probe.payload)-1] != ^byte(worker) {
   t.Fatal("row lost its payload across publication or GC", index)
  }
 }
 if prebuiltFuncCacheLoad(-1) != nil || prebuiltFuncCacheLoad(count) != nil {
  t.Fatal("out of range read did not miss")
 }
 if n := testing.AllocsPerRun(100, func() {
  prebuiltFuncCacheStore(0, unsafe.Pointer(sentinel))
  if prebuiltFuncCacheLoad(0) != unsafe.Pointer(sentinel) { panic("replacement lost") }
  prebuiltFuncCacheStore(0, nil)
  if prebuiltFuncCacheLoad(0) != nil { panic("nil store did not clear row") }
 }); n != 0 { t.Fatal("published-cache access allocated", n) }
}
`
