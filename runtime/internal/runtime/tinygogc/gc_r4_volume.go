//go:build (baremetal && !nogc) || (wasm && llgo.wasm.gc.linear)

package tinygogc

import (
	"unsafe"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
)

// Fork-only instrumentation, never an acceptance implementation. Write fixed
// binary records directly through write(2): formatting or stdio buffering must
// not allocate while the collector lock is held. The workflow decodes them.
type r4GCVolumeCounters struct {
	rootWords, scans, words, largeWords, overflows uint64
}

var r4GCVolume r4GCVolumeCounters
var r4GCRecord [9]uint64

func r4ReportGCVolume() {
	r4GCRecord[0] = 0x52344743564f4c31
	r4GCRecord[1] = uint64(gcNumGC)
	r4GCRecord[2] = uint64(uintptr(metadataStart) - heapStart)
	r4GCRecord[3] = (gcTotalBlocks - gcFreedBlocks) * uint64(bytesPerBlock)
	r4GCRecord[4] = r4GCVolume.rootWords
	r4GCRecord[5] = r4GCVolume.scans
	r4GCRecord[6] = r4GCVolume.words
	r4GCRecord[7] = r4GCVolume.largeWords
	r4GCRecord[8] = r4GCVolume.overflows
	r4Write(1, unsafe.Pointer(&r4GCRecord), unsafe.Sizeof(r4GCRecord))
	// Do not retain plausible heap addresses from the previous record when
	// the conservative collector scans globals during the next collection.
	r4GCRecord = [9]uint64{}
}

//go:linkname r4Write C.write
func r4Write(fd c.Int, data unsafe.Pointer, length uintptr) c.SsizeT
