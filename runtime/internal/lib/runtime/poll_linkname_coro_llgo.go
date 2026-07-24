//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

package runtime

import (
	"unsafe" // required by go:linkname

	cliteos "github.com/goplus/llgo/runtime/internal/clite/os"
	csyscall "github.com/goplus/llgo/runtime/internal/clite/syscall"
	llrt "github.com/goplus/llgo/runtime/internal/runtime"
)

// These values match internal/poll/fd_poll_runtime.go.
const (
	pollNoError        = 0
	pollErrClosing     = 1
	pollErrTimeout     = 2
	pollErrNotPollable = 3
)

const (
	coroPollInterestReadV2  uint32 = 1
	coroPollInterestWriteV2 uint32 = 2

	coroPollResultReadyV2   uint32 = 1
	coroPollResultClosingV2 uint32 = 2
	coroPollResultTimeoutV2 uint32 = 3
)

const (
	coroPollDescClosingV1      uint64 = 1 << 32
	coroPollDescInlineStreamV1 uint64 = 1 << 33
)

// The descriptor owner is an explicitly allocated C object containing scalar
// state only. Go sees one opaque uintptr handle and never converts it to a Go
// pointer, so unrelated descriptors need neither a shared map nor a lock. The
// internal/poll FD reference count delays Free until every operation returns.

//llgo:coro sync
//go:linkname llgoCoroPollDescAllocV1 C.__llgo_runtime_poll_desc_alloc_v1
func llgoCoroPollDescAllocV1(fd int32, inlineStream uint32) uintptr

//llgo:coro sync
//go:linkname llgoCoroPollDescFreeV1 C.__llgo_runtime_poll_desc_free_v1
func llgoCoroPollDescFreeV1(ctx uintptr)

//llgo:coro noblock
//go:linkname llgoCoroPollDescStateV1 C.__llgo_runtime_poll_desc_state_v1
func llgoCoroPollDescStateV1(ctx uintptr) uint64

//llgo:coro noblock
//go:linkname llgoCoroPollDescDeadlineV1 C.__llgo_runtime_poll_desc_deadline_v1
func llgoCoroPollDescDeadlineV1(ctx uintptr, mode int32) int64

//llgo:coro noblock
//go:linkname llgoCoroPollDescSetDeadlineV1 C.__llgo_runtime_poll_desc_set_deadline_v1
func llgoCoroPollDescSetDeadlineV1(ctx uintptr, mode int32, deadline int64)

// llgoCoroPollDescMarkClosingV1 atomically sets closing and returns a state
// word containing its previous value, so exactly one unblock posts wakeups.
//
//llgo:coro noblock
//go:linkname llgoCoroPollDescMarkClosingV1 C.__llgo_runtime_poll_desc_mark_closing_v1
func llgoCoroPollDescMarkClosingV1(ctx uintptr) uint64

func pollDescFD(state uint64) int32 {
	return int32(uint32(state))
}

func pollDescClosing(state uint64) bool {
	return state&coroPollDescClosingV1 != 0
}

func pollDeadline(ctx uintptr, mode int) int64 {
	if ctx == 0 {
		return 0
	}
	return llgoCoroPollDescDeadlineV1(ctx, int32(mode))
}

func pollInterest(mode int) (uint32, bool) {
	switch mode {
	case 'r':
		return coroPollInterestReadV2, true
	case 'w':
		return coroPollInterestWriteV2, true
	default:
		return 0, false
	}
}

func pollAbsoluteDeadline(delay int64) int64 {
	if delay == 0 {
		return 0
	}
	now := runtimeNano()
	if delay < 0 {
		if now > 0 {
			return now
		}
		return 1
	}
	deadline := now + delay
	if deadline <= 0 {
		return int64(^uint64(0) >> 1)
	}
	return deadline
}

func pollDeadlineExpired(deadline int64) bool {
	return deadline > 0 && deadline <= runtimeNano()
}

// pollFDReadinessCapable mirrors the kernel-poller contract expected by
// internal/poll: regular files and directories must stay in blocking mode so
// their syscalls can use the common coroutine worker lowering. poll(2) reports
// such descriptors as perpetually ready, which is not a useful readiness
// source and would otherwise turn blocking file I/O into an executor spin.
// Stat_t.Mode and these POSIX file-type values are shared by the supported
// 64-bit Darwin and Linux clite targets.
func pollFDReadinessCapable(fd int32) (readiness bool, inlineAttempt bool, errno int) {
	var info cliteos.StatT
	result, statErrno := coroPollFstat(fd, &info)
	if uint32(result) == ^uint32(0) {
		if statErrno != 0 {
			return false, false, int(statErrno)
		}
		return false, false, int(csyscall.EOPNOTSUPP)
	}
	switch uint32(info.Mode) & uint32(csyscall.S_IFMT) {
	case uint32(csyscall.S_IFSOCK):
		// MSG_DONTWAIT makes each individual recv/send attempt nonblocking,
		// independently of the open-file-description status flags. Limit that
		// semantic substitution to a kernel-confirmed SOCK_STREAM: datagram,
		// sequenced-packet, and raw sockets retain read/write on the worker path.
		return true, pollCoroFDStreamLeafV1(fd), 0
	case uint32(csyscall.S_IFIFO), uint32(csyscall.S_IFCHR):
		return true, false, 0
	default:
		return false, false, int(csyscall.EOPNOTSUPP)
	}
}

//go:linkname llgoCoroPollWaitV2 llgo.coroPollWait
func llgoCoroPollWaitV2(ctx uintptr, fd int32, interest uint32, deadline int64) uint32

//llgo:coro noblock
//go:linkname llgoCoroPollUpdateDeadlineOrAbortV1 C.__llgo_coro_poll_update_deadline_or_abort_v1
func llgoCoroPollUpdateDeadlineOrAbortV1(ctx uintptr, interest uint32, deadline int64)

//llgo:coro noblock
//go:linkname llgoCoroPollPostClosingOrAbortV1 C.__llgo_coro_poll_post_closing_or_abort_v1
func llgoCoroPollPostClosingOrAbortV1(ctx uintptr, interest uint32)

//llgo:coro contract foreign.v1 progress=executor-safe affinity=any-thread reentry=none memory=by-value
//go:linkname llgoCoroPollFDStreamV1 C.__llgo_runtime_poll_fd_stream_v1
func llgoCoroPollFDStreamV1(fd int32) uint32

func pollCoroFDStreamLeafV1(fd int32) bool {
	return llgoCoroPollFDStreamV1(fd) != 0
}

//llgo:managedlink
//go:linkname poll_runtime_pollServerInit internal/poll.runtime_pollServerInit
func poll_runtime_pollServerInit() {
}

//llgo:managedlink
//go:linkname poll_runtime_pollOpen internal/poll.runtime_pollOpen
func poll_runtime_pollOpen(fd uintptr) (uintptr, int) {
	if fd > uintptr(^uint32(0)>>1) {
		return 0, int(csyscall.EOPNOTSUPP)
	}
	capable, inlineAttempt, errno := pollFDReadinessCapable(int32(fd))
	if !capable {
		return 0, errno
	}
	var inlineStream uint32
	if inlineAttempt {
		inlineStream = 1
	}
	ctx := llgoCoroPollDescAllocV1(int32(fd), inlineStream)
	if ctx == 0 {
		return 0, int(csyscall.ENOMEM)
	}
	return ctx, 0
}

//llgo:managedlink
//go:linkname poll_runtime_pollClose internal/poll.runtime_pollClose
func poll_runtime_pollClose(ctx uintptr) {
	if ctx == 0 {
		return
	}
	if !pollDescClosing(llgoCoroPollDescStateV1(ctx)) {
		throw("runtime: close coroutine polldesc without completed unblock")
		return
	}
	llgoCoroPollDescFreeV1(ctx)
}

//llgo:coro contract foreign.v1 progress=executor-safe affinity=any-thread reentry=none memory=borrow-until-return
//go:linkname llgoCoroPollReadAttemptPackedV1 C.__llgo_runtime_poll_read_attempt_v1
func llgoCoroPollReadAttemptPackedV1(fd int32, address unsafe.Pointer, size uintptr) uint64

//llgo:coro contract foreign.v1 progress=executor-safe affinity=any-thread reentry=none memory=borrow-until-return
//go:linkname llgoCoroPollWriteAttemptPackedV1 C.__llgo_runtime_poll_write_attempt_v1
func llgoCoroPollWriteAttemptPackedV1(fd int32, address unsafe.Pointer, size uintptr) uint64

func pollCoroReadAttemptV1(
	fd int32,
	address unsafe.Pointer,
	size uintptr,
) (result int, errno int, attempted bool) {
	packed := llgoCoroPollReadAttemptPackedV1(fd, address, size)
	result = int(int32(uint32(packed)))
	errno = int(uint32(packed >> 32))
	return result, errno, true
}

func pollCoroWriteAttemptV1(
	fd int32,
	address unsafe.Pointer,
	size uintptr,
) (result int, errno int, attempted bool) {
	packed := llgoCoroPollWriteAttemptPackedV1(fd, address, size)
	result = int(int32(uint32(packed)))
	errno = int(uint32(packed >> 32))
	return result, errno, true
}

func pollCoroAttemptEligible(ctx uintptr, fd int, size int) bool {
	if ctx == 0 || fd < 0 || uintptr(fd) > uintptr(^uint32(0)>>1) || size < 0 {
		return false
	}
	state := llgoCoroPollDescStateV1(ctx)
	return state&coroPollDescInlineStreamV1 != 0 &&
		!pollDescClosing(state) && int32(fd) == pollDescFD(state)
}

//llgo:managedlink
//go:linkname poll_runtime_pollReadAttempt internal/poll.runtime_pollReadAttempt
func poll_runtime_pollReadAttempt(ctx uintptr, fd int, address unsafe.Pointer, size int) (int, int, bool) {
	if !pollCoroAttemptEligible(ctx, fd, size) {
		return 0, 0, false
	}
	return pollCoroReadAttemptV1(int32(fd), address, uintptr(size))
}

//llgo:managedlink
//go:linkname poll_runtime_pollWriteAttempt internal/poll.runtime_pollWriteAttempt
func poll_runtime_pollWriteAttempt(ctx uintptr, fd int, address unsafe.Pointer, size int) (int, int, bool) {
	if !pollCoroAttemptEligible(ctx, fd, size) {
		return 0, 0, false
	}
	return pollCoroWriteAttemptV1(int32(fd), address, uintptr(size))
}

// pollCoroWaitOneV2 is one compiler-owned typed park recipe. Source code hands
// it only the copied scalar descriptor identity and receives one exact
// Ready/Closing/Timeout result after the runtime has retired the source lease.
func pollCoroWaitOneV2(ctx uintptr, fd int32, interest uint32, deadline int64) uint32 {
	return llgoCoroPollWaitV2(ctx, fd, interest, deadline)
}

// pollCoroPrepareWaitV2 resolves the stable scalar descriptor handle before
// suspension, then returns the complete scalar snapshot.
func pollCoroPrepareWaitV2(ctx uintptr, mode int) (fd int32, interest uint32, deadline int64, status int) {
	if ctx == 0 {
		return 0, 0, 0, pollErrNotPollable
	}
	var ok bool
	interest, ok = pollInterest(mode)
	if !ok {
		return 0, 0, 0, pollErrNotPollable
	}
	state := llgoCoroPollDescStateV1(ctx)
	if pollDescClosing(state) {
		return 0, 0, 0, pollErrClosing
	}
	deadline = pollDeadline(ctx, mode)
	if pollDeadlineExpired(deadline) {
		return 0, 0, 0, pollErrTimeout
	}
	return pollDescFD(state), interest, deadline, pollNoError
}

// pollCoroFinishWaitV2 performs a fresh post-resume context lookup. No pointer
// loaded here can be live across pollCoroWaitOneV2's suspension.
func pollCoroFinishWaitV2(ctx uintptr, mode int, result uint32) (status int, retry bool) {
	if ctx == 0 {
		return pollErrNotPollable, false
	}
	state := llgoCoroPollDescStateV1(ctx)
	switch result {
	case coroPollResultReadyV2:
		if pollDescClosing(state) {
			return pollErrClosing, false
		}
		return pollNoError, false
	case coroPollResultClosingV2:
		return pollErrClosing, false
	case coroPollResultTimeoutV2:
		// Close wins over a concurrently delivered timeout, matching Go's
		// pollDesc semantics and preventing a closed descriptor from being
		// surfaced as an ordinary deadline expiry.
		if pollDescClosing(state) {
			return pollErrClosing, false
		}
		// Match Go netpoll's reset-after-expiry rule: a timeout delivered
		// before this waiter runs is stale if its direction now has no
		// deadline or a later future deadline.
		current := pollDeadline(ctx, mode)
		if current == 0 || !pollDeadlineExpired(current) {
			return pollNoError, true
		}
		return pollErrTimeout, false
	default:
		throw("runtime: invalid coroutine poll result")
		return pollErrNotPollable, false
	}
}

// runtime_pollWait retains only the scalar ctx handle across
// pollCoroWaitOneV2. Callers throughout internal/poll and the standard library
// keep their Go blocking style without a Future/Await API or one worker thread
// per wait. internal/poll retains the context allocation until every such wait
// has returned.
//
//llgo:managedlink
//go:linkname poll_runtime_pollWait internal/poll.runtime_pollWait
func poll_runtime_pollWait(ctx uintptr, mode int) int {
	for {
		fd, interest, deadline, status := pollCoroPrepareWaitV2(ctx, mode)
		if status != pollNoError {
			return status
		}
		status, retry := pollCoroFinishWaitV2(ctx, mode, pollCoroWaitOneV2(ctx, fd, interest, deadline))
		if !retry {
			return status
		}
	}
}

//llgo:managedlink
//go:linkname poll_runtime_pollWaitCanceled internal/poll.runtime_pollWaitCanceled
func poll_runtime_pollWaitCanceled(uintptr, int) {
	// Unix does not use this Windows async-I/O cancellation hook.
}

//llgo:managedlink
//go:linkname poll_runtime_pollReset internal/poll.runtime_pollReset
func poll_runtime_pollReset(ctx uintptr, mode int) int {
	if ctx == 0 {
		return pollErrNotPollable
	}
	if _, ok := pollInterest(mode); !ok {
		return pollErrNotPollable
	}
	if pollDescClosing(llgoCoroPollDescStateV1(ctx)) {
		return pollErrClosing
	}
	if pollDeadlineExpired(pollDeadline(ctx, mode)) {
		return pollErrTimeout
	}
	return pollNoError
}

//llgo:managedlink
//go:linkname poll_runtime_pollSetDeadline internal/poll.runtime_pollSetDeadline
func poll_runtime_pollSetDeadline(ctx uintptr, delay int64, mode int) {
	if ctx == 0 {
		return
	}
	state := llgoCoroPollDescStateV1(ctx)
	if pollDescClosing(state) {
		return
	}
	deadline := pollAbsoluteDeadline(delay)
	llgoCoroPollDescSetDeadlineV1(ctx, int32(mode), deadline)
	switch mode {
	case 'r':
		llgoCoroPollUpdateDeadlineOrAbortV1(ctx, coroPollInterestReadV2, deadline)
	case 'w':
		llgoCoroPollUpdateDeadlineOrAbortV1(ctx, coroPollInterestWriteV2, deadline)
	case 'r' + 'w':
		llgoCoroPollUpdateDeadlineOrAbortV1(ctx, coroPollInterestReadV2, deadline)
		llgoCoroPollUpdateDeadlineOrAbortV1(ctx, coroPollInterestWriteV2, deadline)
	}
}

//llgo:managedlink
//go:linkname poll_runtime_pollUnblock internal/poll.runtime_pollUnblock
func poll_runtime_pollUnblock(ctx uintptr) {
	if ctx == 0 {
		return
	}
	state := llgoCoroPollDescMarkClosingV1(ctx)
	if pollDescClosing(state) {
		return
	}
	llgoCoroPollPostClosingOrAbortV1(ctx, coroPollInterestReadV2)
	llgoCoroPollPostClosingOrAbortV1(ctx, coroPollInterestWriteV2)
}

//llgo:managedlink
//go:linkname poll_runtime_isPollServerDescriptor internal/poll.runtime_isPollServerDescriptor
func poll_runtime_isPollServerDescriptor(fd uintptr) bool {
	return llrt.CoroNativePollServerDescriptorV1(fd)
}

func nanotime() int64 { return runtimeNano() }
