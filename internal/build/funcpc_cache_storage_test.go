//go:build !llgo

package build

import (
	stdcontext "context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestFuncForPCCacheStorageContract(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("host preciseGC race projection on native CI hosts")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("missing source location")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	data, err := os.ReadFile(filepath.Join(root, "runtime/internal/lib/runtime/funcpc_cache_default_llgo.go"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, body := range map[string][]byte{"go.mod": []byte("module example.com/llgo-funcpc-cache-contract\n\ngo 1.27.0\n"), "cache.go": data, "storage_test.go": []byte(funcForPCStorageHostTest)} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 90*time.Second)
	defer cancel()
	// Each child process must begin with untouched globals; -count=3 would
	// rerun the cold-start assertion against the first run's published table.
	for repeat := 0; repeat < 3; repeat++ {
		cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin/go"), "test", "-p=1", "-race", "-shuffle=on", "-count=1", "-timeout=70s", "-v", ".")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOMAXPROCS=2")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("preciseGC/storage host projection round %d: %v\n%s", repeat, err, output)
		}
		t.Log(string(output))
	}
}

const funcForPCStorageHostTest = `package runtime

import (
	stdruntime "runtime"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"
)

// Standalone host projection uses the production storage helper. The actual
// runtime cache/Func API still requires separate LLGo native acceptance.
const funcForPCCacheWays = 4

type cacheRetentionProbe struct {
	sequence uint64
	payload [4096]byte
}

func TestColdFuncCacheConcurrentPublicationAndTypedGCRetention(t *testing.T) {
	if funcForPCCacheSets != 1024 || funcForPCCacheWays != 4 || funcForPCCache != nil {
		t.Fatal("expected untouched desktop cache")
	}
	if n := testing.AllocsPerRun(100, func() {
		for index := uintptr(0); index < funcForPCCacheSets; index++ {
			if funcForPCReadSet(index << 4) != nil {
				panic("cold read published storage")
			}
		}
	}); n != 0 {
		t.Fatal("cold cache read allocated", n)
	}
	const workers = 32
	start := make(chan struct{})
	addresses := make(chan [2]uintptr, workers)
	var done sync.WaitGroup
	done.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func(index int) {
			defer done.Done()
			<-start
			set, next := funcForPCInsertionSet(uintptr(index))
			probe := &cacheRetentionProbe{sequence: uint64(index + 1)}
			probe.payload[0] = byte(index)
			probe.payload[len(probe.payload)-1] = ^byte(index)
			atomic.StorePointer(&set[0], unsafe.Pointer(probe))
			atomic.AddUint32(next, 1)
			addresses <- [2]uintptr{uintptr(index), uintptr(unsafe.Pointer(set))}
		}(worker)
	}
	close(start)
	for repeat := 0; repeat < 3; repeat++ {
		stdruntime.GC()
	}
	done.Wait()
	close(addresses)
	var slots [workers]uintptr
	var base uintptr
	for pair := range addresses {
		slots[pair[0]] = pair[1]
		got := pair[1] - pair[0]*unsafe.Sizeof([funcForPCCacheWays]unsafe.Pointer{})
		if base != 0 && got != base {
			t.Fatal("concurrent initialization returned different tables")
		}
		base = got
	}
	// Retain only scalar slot addresses before the collections. Neither the
	// probes nor a typed table reference is retained by this test's locals.
	for repeat := 0; repeat < 3; repeat++ {
		churn := make([][]byte, 256)
		for i := range churn {
			churn[i] = make([]byte, 16384)
		}
		stdruntime.GC()
		stdruntime.KeepAlive(churn)
	}
	for index := range workers {
		set := funcForPCReadSet(uintptr(index) << 4)
		if set == nil || uintptr(unsafe.Pointer(set)) != slots[index] {
			t.Fatal("published slot address changed", index)
		}
		probe := (*cacheRetentionProbe)(atomic.LoadPointer(&set[0]))
		if probe == nil || probe.sequence != uint64(index+1) || probe.payload[0] != byte(index) || probe.payload[len(probe.payload)-1] != ^byte(index) {
			t.Fatal("cache lost pointer-bearing payload across typed GC", index)
		}
		insert, next := funcForPCInsertionSet(uintptr(index))
		if insert != set || atomic.LoadUint32(next) != 1 {
			t.Fatal("publication changed stored cache state", index)
		}
	}
}
`
