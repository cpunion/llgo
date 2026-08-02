package wasmresume

import (
	"testing"
	"unsafe"
)

type testFrameRoots struct {
	blocks map[unsafe.Pointer][]byte
	allocs int
	frees  int
}

type testUnwindFrame struct {
	Frame
	deferFrame unsafe.Pointer
}

func TestFrameArenaABILayout(t *testing.T) {
	pointerSize := unsafe.Sizeof(uintptr(0))
	if got, want := unsafe.Offsetof(Context{}.storage), 2*pointerSize; got != want {
		t.Fatalf("Context storage offset = %d, want %d", got, want)
	}
	if got, want := unsafe.Sizeof(frameBlock{}), 5*pointerSize; got != want {
		t.Fatalf("frameBlock size = %d, want %d", got, want)
	}
	offsets := [...]uintptr{
		unsafe.Offsetof(frameBlock{}.prev),
		unsafe.Offsetof(frameBlock{}.next),
		unsafe.Offsetof(frameBlock{}.begin),
		unsafe.Offsetof(frameBlock{}.end),
		unsafe.Offsetof(frameBlock{}.stackPointer),
	}
	for field, got := range offsets {
		if want := uintptr(field) * pointerSize; got != want {
			t.Fatalf("frameBlock field %d offset = %d, want %d", field, got, want)
		}
	}
}

func (r *testFrameRoots) allocate(size uintptr) unsafe.Pointer {
	block := make([]byte, size)
	if len(block) == 0 {
		return nil
	}
	ptr := unsafe.Pointer(&block[0])
	if r.blocks == nil {
		r.blocks = make(map[unsafe.Pointer][]byte)
	}
	r.blocks[ptr] = block
	r.allocs++
	return ptr
}

func (r *testFrameRoots) release(ptr unsafe.Pointer) {
	if _, ok := r.blocks[ptr]; !ok {
		panic("released unknown root block")
	}
	delete(r.blocks, ptr)
	r.frees++
}

func TestFrameStorageAlignsAndReusesFrames(t *testing.T) {
	var (
		storage frameStorage
		roots   testFrameRoots
	)
	first := storage.allocate(31, 8, roots.allocate)
	second := storage.allocate(64, 64, roots.allocate)
	if first == nil || second == nil {
		t.Fatal("frame allocation failed")
	}
	if uintptr(first)%8 != 0 || uintptr(second)%64 != 0 {
		t.Fatalf("unaligned frames: first=%p second=%p", first, second)
	}
	if roots.allocs != 1 {
		t.Fatalf("root block allocations = %d, want 1", roots.allocs)
	}

	storage.releaseFrame(second, 64)
	reused := storage.allocate(64, 64, roots.allocate)
	if reused != second {
		t.Fatalf("frame was not reused: got %p, want %p", reused, second)
	}
	storage.releaseFrame(reused, 64)
	storage.releaseFrame(first, 31)
	storage.close(roots.release)
	if roots.frees != 1 || len(roots.blocks) != 0 {
		t.Fatalf("released roots = %d, remaining = %d", roots.frees, len(roots.blocks))
	}
}

func TestFrameStorageRetainsAndReleasesSegments(t *testing.T) {
	var (
		storage frameStorage
		roots   testFrameRoots
	)
	first := storage.allocate(defaultFrameBlockSize, 16, roots.allocate)
	second := storage.allocate(128, 16, roots.allocate)
	if first == nil || second == nil || roots.allocs != 2 {
		t.Fatalf("allocations = %d, first=%p second=%p", roots.allocs, first, second)
	}
	storage.releaseFrame(second, 128)
	if roots.frees != 0 {
		t.Fatalf("released child segments = %d, want 0", roots.frees)
	}
	reused := storage.allocate(128, 16, roots.allocate)
	if reused != second || roots.allocs != 2 {
		t.Fatalf("retained segment = %p with %d allocations, want %p with 2", reused, roots.allocs, second)
	}
	storage.releaseFrame(reused, 128)
	storage.releaseFrame(first, defaultFrameBlockSize)
	storage.close(roots.release)
	if roots.frees != 2 || len(roots.blocks) != 0 {
		t.Fatalf("released roots = %d, remaining = %d", roots.frees, len(roots.blocks))
	}
}

func TestFrameStorageFindsFittingRetainedSegment(t *testing.T) {
	var (
		storage frameStorage
		roots   testFrameRoots
	)
	base := storage.allocate(defaultFrameBlockSize, 16, roots.allocate)
	small := storage.allocate(64, 16, roots.allocate)
	large := storage.allocate(2*defaultFrameBlockSize, 16, roots.allocate)
	if base == nil || small == nil || large == nil || roots.allocs != 3 {
		t.Fatalf("allocations = %d, base=%p small=%p large=%p", roots.allocs, base, small, large)
	}

	storage.releaseFrame(large, 2*defaultFrameBlockSize)
	storage.releaseFrame(small, 64)
	reused := storage.allocate(2*defaultFrameBlockSize, 16, roots.allocate)
	if reused != large || roots.allocs != 3 {
		t.Fatalf("fitting retained segment = %p with %d allocations, want %p with 3", reused, roots.allocs, large)
	}
	if storage.current.next == nil || storage.current.next.begin > uintptr(small) || storage.current.next.end < uintptr(small) {
		t.Fatal("smaller retained segment was lost while moving the fitting segment")
	}

	storage.releaseFrame(reused, 2*defaultFrameBlockSize)
	storage.releaseFrame(base, defaultFrameBlockSize)
	storage.close(roots.release)
	if roots.frees != 3 || len(roots.blocks) != 0 {
		t.Fatalf("released roots = %d, remaining = %d", roots.frees, len(roots.blocks))
	}
}

func TestFrameStorageClearsReleasedRoots(t *testing.T) {
	var (
		storage frameStorage
		roots   testFrameRoots
	)
	base := storage.allocate(defaultFrameBlockSize, 16, roots.allocate)
	frame := storage.allocate(64, 16, roots.allocate)
	if base == nil || frame == nil {
		t.Fatal("frame allocation failed")
	}
	bytes := unsafe.Slice((*byte)(frame), 64)
	for i := range bytes {
		bytes[i] = 0xa5
	}
	header := (*uintptr)(unsafe.Pointer(uintptr(frame) - unsafe.Sizeof(uintptr(0))))
	if *header == 0 {
		t.Fatal("frame allocation header was not initialized")
	}

	storage.releaseFrame(frame, 64)
	if *header != 0 {
		t.Fatalf("released frame header = %#x, want 0", *header)
	}
	for i, value := range bytes {
		if value != 0 {
			t.Fatalf("released frame byte %d = %#x, want 0", i, value)
		}
	}

	storage.releaseFrame(base, defaultFrameBlockSize)
	storage.close(roots.release)
}

func TestContextOwnsGeneratedFrameStorage(t *testing.T) {
	var (
		ctx   Context
		roots testFrameRoots
	)
	size := unsafe.Sizeof(testLeafFrame{})
	raw := ctx.AllocateFrame(size, unsafe.Alignof(testLeafFrame{}), roots.allocate)
	if raw == nil {
		t.Fatal("Context.AllocateFrame failed")
	}
	frame := (*testLeafFrame)(raw)
	frame.Descriptor = &Descriptor{FrameSize: size}
	ctx.ReleaseFrame(&frame.Frame)
	ctx.Close(roots.release)
	if roots.allocs != 1 || roots.frees != 1 {
		t.Fatalf("root lifecycle = %d allocs, %d frees", roots.allocs, roots.frees)
	}
}

func TestContextReleaseFrameDiscardsDynamicStorage(t *testing.T) {
	var (
		ctx   Context
		roots testFrameRoots
	)
	const frameSize = uintptr(32)
	raw := ctx.AllocateFrame(frameSize, 8, roots.allocate)
	frame := (*Frame)(raw)
	frame.Descriptor = &Descriptor{FrameSize: frameSize}
	if ctx.AllocateFrame(64, 16, roots.allocate) == nil ||
		ctx.AllocateFrame(defaultFrameBlockSize, 16, roots.allocate) == nil {
		t.Fatal("dynamic frame storage allocation failed")
	}

	ctx.ReleaseFrame(frame)
	reused := ctx.AllocateFrame(frameSize, 8, roots.allocate)
	if reused != raw {
		t.Fatalf("frame storage was not rewound: got %p, want %p", reused, raw)
	}
	ctx.Close(roots.release)
	if roots.allocs != roots.frees || len(roots.blocks) != 0 {
		t.Fatalf("root lifecycle = %d allocs, %d frees, %d remaining",
			roots.allocs, roots.frees, len(roots.blocks))
	}
}

func TestContextUnwindReclaimsChildrenAndRedirectsOwner(t *testing.T) {
	var (
		ctx   Context
		roots testFrameRoots
		token byte
	)
	size := unsafe.Sizeof(testUnwindFrame{})
	align := unsafe.Alignof(testUnwindFrame{})
	owner := (*testUnwindFrame)(ctx.AllocateFrame(size, align, roots.allocate))
	child := (*testUnwindFrame)(ctx.AllocateFrame(size, align, roots.allocate))
	ownerDescriptor := &Descriptor{
		FrameSize:    size,
		UnwindOffset: unsafe.Offsetof(testUnwindFrame{}.deferFrame),
		UnwindPC:     7,
	}
	childDescriptor := &Descriptor{FrameSize: size}
	owner.Descriptor = ownerDescriptor
	owner.deferFrame = unsafe.Pointer(&token)
	child.Parent = &owner.Frame
	child.Descriptor = childDescriptor
	ctx.top = &child.Frame

	if !ctx.Unwind(unsafe.Pointer(&token)) {
		t.Fatal("Context.Unwind did not find the defer owner")
	}
	if ctx.top != &owner.Frame || owner.PC != ownerDescriptor.UnwindPC {
		t.Fatalf("unwind result: top=%p PC=%d", ctx.top, owner.PC)
	}
	reused := ctx.AllocateFrame(size, align, roots.allocate)
	if reused != unsafe.Pointer(child) {
		t.Fatalf("discarded child storage was not reused: got %p, want %p", reused, child)
	}
	ctx.Close(roots.release)
}

func TestContextUnwindRejectsMissingOwner(t *testing.T) {
	var ctx Context
	if ctx.Unwind(nil) || ctx.Unwind(unsafe.Pointer(new(byte))) {
		t.Fatal("Context.Unwind accepted a missing defer owner")
	}
}

func TestContextUnwindIgnoresIncompleteDescriptor(t *testing.T) {
	var (
		ctx   Context
		token byte
		frame testUnwindFrame
	)
	frame.deferFrame = unsafe.Pointer(&token)
	frame.Descriptor = &Descriptor{
		FrameSize:    unsafe.Sizeof(frame),
		UnwindOffset: unsafe.Offsetof(frame.deferFrame),
	}
	ctx.top = &frame.Frame
	if ctx.Unwind(unsafe.Pointer(&token)) {
		t.Fatal("Context.Unwind accepted a descriptor without an unwind PC")
	}
}

func TestContextKeepsGeneratedABIPrefix(t *testing.T) {
	if got, want := unsafe.Offsetof(Context{}.storage), 2*unsafe.Sizeof(uintptr(0)); got != want {
		t.Fatalf("Context storage offset = %d, want %d", got, want)
	}
}

func TestFrameStorageRejectsInvalidOperations(t *testing.T) {
	var (
		storage frameStorage
		roots   testFrameRoots
	)
	if storage.allocate(0, 8, roots.allocate) != nil ||
		storage.allocate(8, 3, roots.allocate) != nil ||
		storage.allocate(^uintptr(0), 8, roots.allocate) != nil ||
		storage.allocate(8, 8, nil) != nil {
		t.Fatal("invalid allocation was accepted")
	}
	if storage.allocate(8, 8, func(uintptr) unsafe.Pointer { return nil }) != nil {
		t.Fatal("failed root allocation returned a frame")
	}

	storage.close(roots.release)
}

func TestFrameStorageRejectsInvalidReleaseState(t *testing.T) {
	assertPanic := func(name string, operation func()) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("operation did not panic")
				}
			}()
			operation()
		})
	}

	assertPanic("empty", func() {
		var storage frameStorage
		storage.releaseFrame(unsafe.Pointer(new(byte)), 1)
	})

	var (
		storage frameStorage
		roots   testFrameRoots
	)
	frame := storage.allocate(8, 8, roots.allocate)
	assertPanic("nil frame", func() {
		storage.releaseFrame(nil, 8)
	})
	assertPanic("zero size", func() {
		storage.releaseFrame(frame, 0)
	})
	assertPanic("foreign frame", func() {
		storage.releaseFrame(unsafe.Pointer(new(byte)), 1)
	})
	assertPanic("invalid header", func() {
		header := unsafe.Pointer(uintptr(frame) - unsafe.Sizeof(uintptr(0)))
		*(*uintptr)(header) = 0
		storage.releaseFrame(frame, 8)
	})
	storage.close(roots.release)

	var noRelease frameStorage
	noRelease.allocate(8, 8, roots.allocate)
	assertPanic("close without reclaimer", func() {
		noRelease.close(nil)
	})
	noRelease.close(roots.release)
}

func TestContextRejectsFrameWithoutDescriptor(t *testing.T) {
	var ctx Context
	defer func() {
		if recover() == nil {
			t.Fatal("ReleaseFrame accepted an untyped frame")
		}
	}()
	ctx.ReleaseFrame(&Frame{})
}

func BenchmarkFrameStorageHotAllocateRelease(b *testing.B) {
	var (
		storage frameStorage
		roots   testFrameRoots
	)
	frame := storage.allocate(64, 16, roots.allocate)
	storage.releaseFrame(frame, 64)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame = storage.allocate(64, 16, roots.allocate)
		storage.releaseFrame(frame, 64)
	}
}

func BenchmarkFrameStorageRetainedOverflow(b *testing.B) {
	var (
		storage frameStorage
		roots   testFrameRoots
	)
	base := storage.allocate(defaultFrameBlockSize, 16, roots.allocate)
	frame := storage.allocate(64, 16, roots.allocate)
	storage.releaseFrame(frame, 64)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame = storage.allocate(64, 16, roots.allocate)
		storage.releaseFrame(frame, 64)
	}
	b.StopTimer()
	storage.releaseFrame(base, defaultFrameBlockSize)
	storage.close(roots.release)
}
