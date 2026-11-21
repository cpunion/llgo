package main

import (
	"fmt"
	"github.com/xgo-dev/llgo/runtime/internal/coro"
	"runtime"
	"unsafe"
)

func main() {
	var id coro.OperationID
	if unsafe.Sizeof(id) != 8 || coro.ExecutorFleetCapacity != 8 {
		panic("wire profile")
	}
	for _, capacity := range []uint32{coro.TimerRegistrationMaximumCapacity, coro.PollOperationMaximumCapacity, coro.ManualOperationMaximumCapacity, coro.WorkerOperationMaximumCapacity, coro.ChannelOperationMaximumCapacity} {
		if capacity != 32704 {
			panic("logical capacity")
		}
	}
	page := uint32(coro.ManualOperationPageCapacity)
	block := uint32(unsafe.Sizeof(coro.OperationPageDirectoryBlock{}) / unsafe.Sizeof(uintptr(0)))
	if !((page == 4 && block == 256) || (page == 64 && block == 64)) {
		panic("physical profile")
	}
	owner := new(coro.P)
	source := new(coro.ManualOperationSource)
	if !coro.BindManualOperationSource(source, owner) {
		panic("bind")
	}
	for index := uint32(0); index < block+1; index++ {
		var directory *coro.OperationPageDirectoryBlock
		if index%block == 0 {
			directory = new(coro.OperationPageDirectoryBlock)
		}
		if !coro.AttachManualOperationPage(source, owner, new(coro.ManualOperationPage), directory) {
			panic("attach")
		}
	}
	capacity := (block + 2) * page
	if coro.ManualOperationConfiguredCapacity(source) != capacity {
		panic("growth capacity")
	}
	runtime.GC()
	// The published directory must be the only root for the attached pages.
	// Churn uses the same page type and writes only its frozen uint32 prefix;
	// pointer fields stay zero so every churn object remains GC-valid.
	churn := make([]*coro.ManualOperationPage, 1000)
	for index := range churn {
		churn[index] = new(coro.ManualOperationPage)
		*(*uint32)(unsafe.Pointer(churn[index])) = 0x5a5a5a5a
	}
	runtime.GC()
	if coro.ManualOperationConfiguredCapacity(source) != capacity || !coro.UnbindManualOperationSource(source, owner) || !source.CanRelease() {
		panic("expanded page GC or release")
	}
	for _, page := range churn {
		if *(*uint32)(unsafe.Pointer(page)) != 0x5a5a5a5a {
			panic("same-kind churn canary")
		}
	}
	fmt.Println("page growth gc ok page", page, "block", block, "capacity", capacity, "same-kind")
}
