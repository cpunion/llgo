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

// The collector itself requires LLGo's C runtime. Compile its actual metadata
// operations with a small host arena and C-memory shims, so exhaustive state
// tests do not alter the collector currently running the test process.
func TestWasmGCMetadata(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join("..", "..", "runtime", "internal", "runtime", "tinygogc", "gc_tinygo.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	functions := map[string]bool{
		"gcFindHead": true, "gcFindNext": true, "gcStateByteOf": true, "gcStateFromByte": true,
		"gcStateOf": true, "gcAddressOf": true, "gcPointerOf": true,
		"gcMarkFree": true, "gcUnmark": true, "sweep": true, "finishMark": true,
	}
	var source bytes.Buffer
	source.WriteString(gcMetadataTestSource)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !functions[fn.Name.Name] {
			continue
		}
		if err := format.Node(&source, fset, fn); err != nil {
			t.Fatal(err)
		}
		source.WriteByte('\n')
		delete(functions, fn.Name.Name)
	}
	if len(functions) != 0 {
		t.Fatalf("missing collector operations: %v", functions)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata_test.go")
	if err := os.WriteFile(path, source.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 45*time.Second)
	defer cancel()
	architectures := []string{runtime.GOARCH}
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		// Linux CI can execute both pointer widths. Metadata word loads and
		// block rounding must not be validated only with eight-byte words.
		architectures = append(architectures, "386")
	}
	for _, arch := range architectures {
		cmd := exec.CommandContext(ctx, "go", "test", "-count=1", "-timeout=30s", "-bench=BenchmarkMarkScan", "-benchtime=50ms", path)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOARCH="+arch, "CGO_ENABLED=0")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("collector metadata operations (%s): %v\n%s", arch, err, output)
		}
		t.Logf("collector metadata checks (%s) and host-only scan timings:\n%s", arch, output)
	}
}

const gcMetadataTestSource = `package metadata
import (
  "testing"
  "unsafe"
)
const (
  blockStateFree uint8 = iota
  blockStateHead
  blockStateTail
  blockStateMark
  blockStateMask = 3
  blockStateByteAllTails = 0xaa
  wordsPerBlock = 4
  bytesPerBlock = wordsPerBlock * unsafe.Sizeof(uintptr(0))
  stateBits = 2
  blocksPerStateByte = 4
)
var heapStart, endBlock uintptr
var metadataStart unsafe.Pointer
var gcFreedBlocks, gcFrees, profileCalls uint64
var profileHash uintptr
var markStackOverflow bool
var markVisits []uintptr
var markVisitHook func(uintptr)
func startMark(block uintptr) {
  markVisits = append(markVisits, block)
  if markVisitHook != nil { markVisitHook(block) }
}
var c = struct {
  Str func(string) string
  Memset func(unsafe.Pointer, int, uintptr)
}{
  func(s string) string { return s },
  func(p unsafe.Pointer, value int, count uintptr) {
    for i := uintptr(0); i < count; i++ { *(*byte)(unsafe.Add(p, i)) = byte(value) }
  },
}
func gcPanic(s string) { panic(s) }
func memProfileFree(address uintptr) {
  profileCalls++
  profileHash = profileHash*31 + address
}
func TestMarkScanAlignmentAndOverflow(t *testing.T) {
  const wordSize = unsafe.Sizeof(uintptr(0))
  const wordBlocks = wordSize*blocksPerStateByte
  for alignment := uintptr(0); alignment < wordSize; alignment++ {
    data := make([]byte, alignment+5*wordSize+1)
    metadataStart = unsafe.Pointer(&data[alignment])
    setState := func(block uintptr, state byte) {
      p := (*byte)(unsafe.Add(metadataStart, block/4))
      *p = *p &^ (3 << ((block%4)*2)) | state << ((block%4)*2)
    }
    for length := uintptr(1); length <= 4*wordBlocks+3; length++ {
      endBlock = length
      for _, fill := range []byte{0, 0xaa, 0x55, 0xee, 0xe4} {
        for i := range data { data[i] = fill }
        // In-range marks at different byte/word offsets; the mark just
        // beyond the logical heap end must never be visited.
        setState(length/3, blockStateMark)
        setState(length-1, blockStateMark)
        setState(length, blockStateMark)
        var want []uintptr
        for block := uintptr(0); block < length; block++ {
          if gcStateOf(block) == blockStateMark { want = append(want, block) }
        }
        markVisits = markVisits[:0]
        markStackOverflow = true
        finishMark()
        if !equalVisits(markVisits, want) {
          t.Fatalf("align=%d length=%d fill=%x visits=%v want=%v", alignment, length, fill, markVisits, want)
        }
      }
    }
    for i := range data { data[i] = 0x55 }
    endBlock = 3*wordBlocks
    first := wordBlocks+1
    setState(first, blockStateMark)
    triggered := false
    markVisitHook = func(block uintptr) {
      if !triggered && block == first {
        triggered = true
        // A late overflow can publish work both before and after the
        // current scanner position. The earlier mark needs another pass.
        setState(1, blockStateMark)
        setState(first+1, blockStateMark)
        markStackOverflow = true
      }
    }
    markVisits = markVisits[:0]
    markStackOverflow = true
    finishMark()
    markVisitHook = nil
    want := []uintptr{first, first+1, 1, first, first+1}
    if !equalVisits(markVisits, want) { t.Fatalf("overflow: got=%v want=%v", markVisits, want) }
  }
}
func equalVisits(got, want []uintptr) bool {
  if len(got) != len(want) { return false }
  for i := range got { if got[i] != want[i] { return false } }
  return true
}
func BenchmarkMarkScan(b *testing.B) {
  for _, workload := range []struct { name string; blocks, spacing uintptr }{
    {"stack-tails", 1<<20, 1<<15},
    {"dense-heads", 1<<12, 4},
  } {
    data := make([]byte, workload.blocks/4)
    for i := range data { data[i] = blockStateByteAllTails }
    for block := uintptr(0); block < workload.blocks; block += workload.spacing {
      data[block/4] |= 1 << ((block%4)*2)
    }
    metadataStart = unsafe.Pointer(&data[0])
    endBlock = workload.blocks
    for _, mode := range []string{"blocks", "packed"} {
      b.Run(workload.name+"/"+mode, func(b *testing.B) {
        markVisits = make([]uintptr, 0, workload.blocks/workload.spacing)
        b.ResetTimer()
        for iteration := 0; iteration < b.N; iteration++ {
          markVisits = markVisits[:0]
          if mode == "packed" {
            markStackOverflow = true
            finishMark()
          } else {
            // Original overflow-pass traversal, with the same startMark
            // stand-in. This measures traversal, not complete collections.
            for block := uintptr(0); block < endBlock; block++ {
              if gcStateOf(block) == blockStateMark { startMark(block) }
            }
          }
        }
      })
    }
  }
}
func TestTailScanAlignmentAndBounds(t *testing.T) {
  const wordSize = unsafe.Sizeof(uintptr(0))
  const wordBlocks = wordSize*blocksPerStateByte
  for alignment := uintptr(0); alignment < wordSize; alignment++ {
    // Leave more tails after the logical heap end. Skipping an unbounded
    // word would then return the wrong end even inside the backing array.
    data := make([]byte, alignment + 6*wordSize + 4)
    metadataStart = unsafe.Pointer(&data[alignment])
    for head := uintptr(0); head < 2*wordBlocks; head++ {
      for span := uintptr(1); span <= 2*wordBlocks+3; span++ {
        for _, state := range []uint8{blockStateHead, blockStateMark} {
          for i := range data { data[i] = blockStateByteAllTails }
          p := (*byte)(unsafe.Add(metadataStart, head/blocksPerStateByte))
          *p = *p &^ (3 << ((head%4)*2)) | state << ((head%4)*2)
          limit := head+span
          for _, boundary := range []uint8{blockStateTail, blockStateFree, blockStateHead, blockStateMark} {
            endBlock = limit
            if boundary != blockStateTail {
              endBlock += wordBlocks
              p := (*byte)(unsafe.Add(metadataStart, limit/blocksPerStateByte))
              *p = *p &^ (3 << ((limit%4)*2)) | boundary << ((limit%4)*2)
            }
            for block := head; block < limit; block++ {
              if got := gcFindHead(block); got != head {
                t.Fatalf("head: align=%d head=%d span=%d block=%d got=%d", alignment, head, span, block, got)
              }
              if got := gcFindNext(block); got != limit {
                t.Fatalf("end: align=%d head=%d span=%d block=%d state=%d got=%d want=%d", alignment, head, span, block, boundary, got, limit)
              }
            }
          }
        }
      }
    }
  }
}
func TestPackedStates(t *testing.T) {
  const capacity = 8
  // Prefix/suffix words detect out-of-range metadata or payload writes.
  arena := make([]uintptr, 1 + capacity*wordsPerBlock + 2)
  expected := make([]uintptr, len(arena))
  heapStart = uintptr(unsafe.Pointer(&arena[1]))
  metadataStart = unsafe.Pointer(&arena[1 + capacity*wordsPerBlock])
  for pattern := uint32(0); pattern <= 0xffff; pattern++ {
    for length := uintptr(1); length <= capacity; length++ {
      for i := range arena { arena[i] = ^uintptr(0) }
      *(*byte)(metadataStart) = byte(pattern)
      *(*byte)(unsafe.Add(metadataStart, 1)) = byte(pattern >> 8)
      copy(expected, arena)
      endBlock = length
      markVisits = markVisits[:0]
      markStackOverflow = true
      finishMark()
      count := 0
      for block := uintptr(0); block < length; block++ {
        if uint8(pattern >> (block*2)) & 3 == blockStateMark {
          if count >= len(markVisits) || markVisits[count] != block {
            t.Fatalf("mark scan: pattern=%04x length=%d visits=%v", pattern, length, markVisits)
          }
          count++
        }
      }
      if count != len(markVisits) { t.Fatalf("extra mark visits: %v", markVisits) }
      for start := uintptr(0); start < length; start++ {
        want := start
        state := uint8(pattern >> (want*2)) & 3
        if state == blockStateHead || state == blockStateMark { want++ }
        for want < length && uint8(pattern >> (want*2)) & 3 == blockStateTail { want++ }
        if got := gcFindNext(start); got != want {
          t.Fatalf("find next: pattern=%04x length=%d start=%d got=%d want=%d", pattern, length, start, got, want)
        }
      }
      expectedMeta := unsafe.Slice((*byte)(unsafe.Pointer(&expected[1 + capacity*wordsPerBlock])), int(unsafe.Sizeof(uintptr(0))))
      var wantFree uintptr
      var wantFreed, wantObjects uint64
      var wantHash uintptr
      dead := false
      for block := uintptr(0); block < length; block++ {
        shift := (block%4)*2
        state := uint8(pattern >> (block*2)) & 3
        collect := false
        switch state {
        case blockStateHead:
          dead, collect = true, true
          wantObjects++
          wantHash = wantHash*31 + heapStart + block*bytesPerBlock
        case blockStateTail:
          collect = dead
        case blockStateMark:
          dead = false
          expectedMeta[block/4] &^= 2 << shift
        case blockStateFree:
          wantFree += bytesPerBlock
        }
        if collect {
          expectedMeta[block/4] &^= 3 << shift
          wantFreed++
          wantFree += bytesPerBlock
          for word := 0; word < wordsPerBlock; word++ { expected[1+int(block)*wordsPerBlock+word] = 0 }
        }
      }
      gcFreedBlocks, gcFrees, profileCalls, profileHash = 7, 11, 0, 0
      gotFree := sweep()
      if gotFree != wantFree || gcFreedBlocks != wantFreed+7 || gcFrees != wantObjects+11 || profileCalls != wantObjects || profileHash != wantHash {
        t.Fatalf("sweep counters: pattern=%04x length=%d free=%d/%d blocks=%d/%d objects=%d/%d profiles=%d hash=%d/%d", pattern, length, gotFree, wantFree, gcFreedBlocks, wantFreed+7, gcFrees, wantObjects+11, profileCalls, profileHash, wantHash)
      }
      for i := range arena {
        if arena[i] != expected[i] { t.Fatalf("sweep data: pattern=%04x length=%d word=%d got=%x want=%x", pattern, length, i, arena[i], expected[i]) }
      }
    }
  }
}
`
