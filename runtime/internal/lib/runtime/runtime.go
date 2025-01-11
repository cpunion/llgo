/*
 * Copyright (c) 2024 The GoPlus Authors (goplus.org). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package runtime

// llgo:skip GC Goexit LockOSThread UnlockOSThread
import (
	"unsafe"

	"github.com/goplus/llgo/runtime/abi"
	"github.com/goplus/llgo/runtime/internal/clite/pthread"
)

// GOROOT returns the root of the Go tree. It uses the
// GOROOT environment variable, if set at process start,
// or else the root used during the Go build.
func GOROOT() string {
	/*
		s := gogetenv("GOROOT")
		if s != "" {
			return s
		}
		return defaultGOROOT
	*/
	panic("todo: GOROOT")
}

func GC() {
	panic("todo: GC")
}

func Goexit() {
	pthread.Exit(nil)
}

func LockOSThread() {
}

func UnlockOSThread() {
}

//go:linkname memmove C.memmove
func memmove(dst, src unsafe.Pointer, size uintptr)

func getcallerpc() uintptr {
	panic("todo: getcallerpc")
}

func getcallersp() uintptr {
	panic("todo: getcallersp")
}

func systemstack(fn func()) {
	fn()
}

func mcall(fn func(*unsafe.Pointer)) {
	fn(nil)
	panic("todo: mcall")
}

func entersyscall() {
	panic("todo: entersyscall")
}

func exitsyscall() {
	panic("todo: exitsyscall")
}

type RegArgs struct{}

func reflectcall(stackArgsType *abi.Type, fn, stackArgs unsafe.Pointer, stackArgsSize, stackRetOffset, frameSize uint32, regArgs *RegArgs) {
	panic("todo: reflectcall")
}

func memhash(p unsafe.Pointer, h, s uintptr) uintptr {
	panic("todo: memhash")
}

func memequal(x, y *any, size uintptr) bool {
	panic("todo: memequal")
}

func memclrNoHeapPointers(ptr unsafe.Pointer, n uintptr) {
	panic("todo: memclrNoHeapPointers")
}

func procyield(cycles uint32) {
	panic("todo: procyield")
}

func asyncPreempt() {
	panic("todo: asyncPreempt")
}

// System calls
func exit_trampoline() {
	panic("todo: exit_trampoline")
}

func fcntl_trampoline() {
	panic("todo: fcntl_trampoline")
}

func kevent_trampoline() {
	panic("todo: kevent_trampoline")
}

func kqueue_trampoline() {
	panic("todo: kqueue_trampoline")
}

func mach_vm_region_trampoline() {
	panic("todo: mach_vm_region_trampoline")
}

func madvise_trampoline() {
	panic("todo: madvise_trampoline")
}

func mlock_trampoline() {
	panic("todo: mlock_trampoline")
}

func mmap_trampoline() {
	panic("todo: mmap_trampoline")
}

func munmap_trampoline(addr unsafe.Pointer, n uintptr) int32 {
	panic("todo: munmap_trampoline")
}

func nanotime_trampoline() int64 {
	panic("todo: nanotime_trampoline")
}

//go:nosplit
func mstart() {
	panic("todo: mstart")
}

//go:nosplit
func mstart_stub() {
	panic("todo: mstart_stub")
}

func pipe_trampoline(fd *[2]int32) int32 {
	panic("todo: pipe_trampoline")
}

func proc_regionfilename_trampoline(pid int32, address uint64, buffer *byte, buffersize int32) int32 {
	panic("todo: proc_regionfilename_trampoline")
}

func pthread_attr_getstacksize_trampoline(attr unsafe.Pointer, size *uintptr) int32 {
	panic("todo: pthread_attr_getstacksize_trampoline")
}

func pthread_attr_init_trampoline(attr unsafe.Pointer) int32 {
	panic("todo: pthread_attr_init_trampoline")
}

func pthread_attr_setdetachstate_trampoline(attr unsafe.Pointer, state int32) int32 {
	panic("todo: pthread_attr_setdetachstate_trampoline")
}

func pthread_cond_init_trampoline(cond unsafe.Pointer, attr unsafe.Pointer) int32 {
	panic("todo: pthread_cond_init_trampoline")
}

func pthread_cond_signal_trampoline(cond unsafe.Pointer) int32 {
	panic("todo: pthread_cond_signal_trampoline")
}

func pthread_cond_timedwait_relative_np_trampoline(cond unsafe.Pointer, mutex unsafe.Pointer, timeout unsafe.Pointer) int32 {
	panic("todo: pthread_cond_timedwait_relative_np_trampoline")
}

func pthread_cond_wait_trampoline(cond unsafe.Pointer, mutex unsafe.Pointer) int32 {
	panic("todo: pthread_cond_wait_trampoline")
}

func pthread_create_trampoline(thread *pthread.Thread, attr unsafe.Pointer, start_routine unsafe.Pointer, arg unsafe.Pointer) int32 {
	panic("todo: pthread_create_trampoline")
}

func pthread_kill_trampoline(thread pthread.Thread, sig int32) int32 {
	panic("todo: pthread_kill_trampoline")
}

func pthread_mutex_init_trampoline(mutex unsafe.Pointer, attr unsafe.Pointer) int32 {
	panic("todo: pthread_mutex_init_trampoline")
}

func pthread_mutex_lock_trampoline(mutex unsafe.Pointer) int32 {
	panic("todo: pthread_mutex_lock_trampoline")
}

func pthread_mutex_unlock_trampoline(mutex unsafe.Pointer) int32 {
	panic("todo: pthread_mutex_unlock_trampoline")
}

func raise_trampoline(sig int32) int32 {
	panic("todo: raise_trampoline")
}

func raiseproc_trampoline(sig int32) {
	panic("todo: raiseproc_trampoline")
}

func read_trampoline(fd int32, p unsafe.Pointer, n int32) int32 {
	panic("todo: read_trampoline")
}

func setitimer_trampoline(mode int32, new, old unsafe.Pointer) int32 {
	panic("todo: setitimer_trampoline")
}

//go:nosplit
func sigtramp() {
	panic("todo: sigtramp")
}

//go:nosplit
func cgoSigtramp() {
	panic("todo: cgoSigtramp")
}

func sigaction_trampoline(sig uint32, new, old unsafe.Pointer) int32 {
	panic("todo: sigaction_trampoline")
}

func sigprocmask_trampoline(how int32, new, old unsafe.Pointer) int32 {
	panic("todo: sigprocmask_trampoline")
}

func usleep_trampoline(usec uint32) int32 {
	panic("todo: usleep_trampoline")
}

func walltime_trampoline(sec *int64, nsec *int32) {
	panic("todo: walltime_trampoline")
}

func write_trampoline(fd uintptr, p unsafe.Pointer, n int32) int32 {
	panic("todo: write_trampoline")
}

func FuncPCABIInternal(f interface{}) uintptr {
	panic("todo: FuncPCABIInternal")
}

func FuncPCABI0(f interface{}) uintptr {
	panic("todo: FuncPCABI0")
}

func asmcgocall_no_g(fn, arg unsafe.Pointer) {
	panic("todo: asmcgocall_no_g")
}

func asmcgocall(fn, arg unsafe.Pointer) int32 {
	panic("todo: asmcgocall")
}

func getfp() uintptr {
	panic("todo: getfp")
}

func publicationBarrier() {
	panic("todo: publicationBarrier")
}

type guintptr uintptr

type gobuf struct {
	// The offsets of sp, pc, and g are known to (hard-coded in) libmach.
	//
	// ctxt is unusual with respect to GC: it may be a
	// heap-allocated funcval, so GC needs to track it, but it
	// needs to be set and cleared from assembly, where it's
	// difficult to have write barriers. However, ctxt is really a
	// saved, live register, and we only ever exchange it between
	// the real register and the gobuf. Hence, we treat it as a
	// root during stack scanning, which means assembly that saves
	// and restores it doesn't need write barriers. It's still
	// typed as a pointer so that any other writes from Go get
	// write barriers.
	sp   uintptr
	pc   uintptr
	g    guintptr
	ctxt unsafe.Pointer
	ret  uintptr
	lr   uintptr
	bp   uintptr // for framepointer-enabled architectures
}

func gogo(buf *gobuf) {
	panic("todo: gogo")
}

func Stack() []byte {
	panic("todo: Stack")
}

type _panic struct {
	argp unsafe.Pointer // pointer to arguments of deferred call run during panic; cannot move - known to liblink
	arg  any            // argument to panic
	link *_panic        // link to earlier panic

	// startPC and startSP track where _panic.start was called.
	startPC uintptr
	startSP unsafe.Pointer

	// The current stack frame that we're running deferred calls for.
	sp unsafe.Pointer
	lr uintptr
	fp unsafe.Pointer

	// retpc stores the PC where the panic should jump back to, if the
	// function last returned by _panic.next() recovers the panic.
	retpc uintptr

	// Extra state for handling open-coded defers.
	deferBitsPtr *uint8
	slotsPtr     unsafe.Pointer

	recovered   bool // whether this panic has been recovered
	goexit      bool
	deferreturn bool
}
