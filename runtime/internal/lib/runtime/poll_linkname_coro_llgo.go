//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

package runtime

import (
	"unsafe" // required by go:linkname

	cliteos "github.com/goplus/llgo/runtime/internal/clite/os"
	csyscall "github.com/goplus/llgo/runtime/internal/clite/syscall"
	latomic "github.com/goplus/llgo/runtime/internal/lib/sync/atomic"
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

type llgoPollDesc struct {
	fd           int32
	closing      uint32
	rd           int64
	wd           int64
	inlineStream bool
}

var pollDescRoots map[uintptr]*llgoPollDesc
var pollDescNext uintptr

func init() {
	pollDescRoots = make(map[uintptr]*llgoPollDesc)
}

func pollRootAdd(pd *llgoPollDesc) uintptr {
	if pd == nil {
		return 0
	}
	pollDescNext++
	ctx := pollDescNext
	if ctx == 0 || pollDescRoots[ctx] != nil {
		return 0
	}
	pollDescRoots[ctx] = pd
	return ctx
}

func pollRootGet(ctx uintptr) *llgoPollDesc {
	if ctx == 0 {
		return nil
	}
	pd := pollDescRoots[ctx]
	return pd
}

func pollRootDel(ctx uintptr) {
	if ctx == 0 {
		return
	}
	delete(pollDescRoots, ctx)
}

func pollDeadline(pd *llgoPollDesc, mode int) int64 {
	if pd == nil {
		return 0
	}
	switch mode {
	case 'r':
		return latomic.LoadInt64(&pd.rd)
	case 'w':
		return latomic.LoadInt64(&pd.wd)
	default:
		rd := latomic.LoadInt64(&pd.rd)
		wd := latomic.LoadInt64(&pd.wd)
		if rd == 0 {
			return wd
		}
		if wd == 0 || rd < wd {
			return rd
		}
		return wd
	}
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
func llgoCoroPollWaitV2(fd int32, interest uint32, deadline int64) uint32

//llgo:coro noblock
//go:linkname llgoCoroPollUpdateDeadlineOrAbortV1 C.__llgo_coro_poll_update_deadline_or_abort_v1
func llgoCoroPollUpdateDeadlineOrAbortV1(fd int32, interest uint32, deadline int64)

//llgo:coro noblock
//go:linkname llgoCoroPollPostClosingOrAbortV1 C.__llgo_coro_poll_post_closing_or_abort_v1
func llgoCoroPollPostClosingOrAbortV1(fd int32, interest uint32)

//llgo:coro contract foreign.v1 progress=executor-safe affinity=any-thread reentry=none memory=by-value
//go:linkname llgoCoroPollFDStreamV1 C.__llgo_runtime_poll_fd_stream_v1
func llgoCoroPollFDStreamV1(fd int32) uint32

func pollCoroFDStreamLeafV1(fd int32) bool {
	return llgoCoroPollFDStreamV1(fd) != 0
}

//go:linkname poll_runtime_pollServerInit internal/poll.runtime_pollServerInit
func poll_runtime_pollServerInit() {
}

//go:linkname poll_runtime_pollOpen internal/poll.runtime_pollOpen
func poll_runtime_pollOpen(fd uintptr) (uintptr, int) {
	if fd > uintptr(^uint32(0)>>1) {
		return 0, int(csyscall.EOPNOTSUPP)
	}
	capable, inlineAttempt, errno := pollFDReadinessCapable(int32(fd))
	if !capable {
		return 0, errno
	}
	pd := &llgoPollDesc{fd: int32(fd), inlineStream: inlineAttempt}
	ctx := pollRootAdd(pd)
	if ctx == 0 {
		return 0, int(csyscall.ENOMEM)
	}
	return ctx, 0
}

//go:linkname poll_runtime_pollClose internal/poll.runtime_pollClose
func poll_runtime_pollClose(ctx uintptr) {
	pd := pollRootGet(ctx)
	if pd == nil {
		return
	}
	if latomic.LoadUint32(&pd.closing) == 0 {
		throw("runtime: close coroutine polldesc without completed unblock")
		return
	}
	pollRootDel(ctx)
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

func pollCoroAttemptEligible(pd *llgoPollDesc, ctx uintptr, fd int, size int) bool {
	if pd == nil || !pd.inlineStream || ctx == 0 || fd < 0 ||
		uintptr(fd) > uintptr(^uint32(0)>>1) || int32(fd) != pd.fd ||
		size < 0 ||
		latomic.LoadUint32(&pd.closing) != 0 {
		return false
	}
	return true
}

//go:linkname poll_runtime_pollReadAttempt internal/poll.runtime_pollReadAttempt
func poll_runtime_pollReadAttempt(ctx uintptr, fd int, address unsafe.Pointer, size int) (int, int, bool) {
	pd := pollRootGet(ctx)
	if !pollCoroAttemptEligible(pd, ctx, fd, size) {
		return 0, 0, false
	}
	return pollCoroReadAttemptV1(pd.fd, address, uintptr(size))
}

//go:linkname poll_runtime_pollWriteAttempt internal/poll.runtime_pollWriteAttempt
func poll_runtime_pollWriteAttempt(ctx uintptr, fd int, address unsafe.Pointer, size int) (int, int, bool) {
	pd := pollRootGet(ctx)
	if !pollCoroAttemptEligible(pd, ctx, fd, size) {
		return 0, 0, false
	}
	return pollCoroWriteAttemptV1(pd.fd, address, uintptr(size))
}

// pollCoroWaitOneV2 is one compiler-owned typed park recipe. Source code hands
// it only the copied scalar descriptor identity and receives one exact
// Ready/Closing/Timeout result after the runtime has retired the source lease.
func pollCoroWaitOneV2(fd int32, interest uint32, deadline int64) uint32 {
	return llgoCoroPollWaitV2(fd, interest, deadline)
}

// pollCoroPrepareWaitV2 resolves the stable scalar descriptor handle through
// the typed root catalog before suspension, then returns the complete scalar
// snapshot.
func pollCoroPrepareWaitV2(ctx uintptr, mode int) (fd int32, interest uint32, deadline int64, status int) {
	pd := pollRootGet(ctx)
	if pd == nil {
		return 0, 0, 0, pollErrNotPollable
	}
	var ok bool
	interest, ok = pollInterest(mode)
	if !ok {
		return 0, 0, 0, pollErrNotPollable
	}
	if latomic.LoadUint32(&pd.closing) != 0 {
		return 0, 0, 0, pollErrClosing
	}
	deadline = pollDeadline(pd, mode)
	if pollDeadlineExpired(deadline) {
		return 0, 0, 0, pollErrTimeout
	}
	return pd.fd, interest, deadline, pollNoError
}

// pollCoroFinishWaitV2 performs a fresh post-resume catalog lookup. No pointer
// loaded here can be live across pollCoroWaitOneV2's suspension.
func pollCoroFinishWaitV2(ctx uintptr, mode int, result uint32) (status int, retry bool) {
	pd := pollRootGet(ctx)
	if pd == nil {
		return pollErrNotPollable, false
	}
	switch result {
	case coroPollResultReadyV2:
		if latomic.LoadUint32(&pd.closing) != 0 {
			return pollErrClosing, false
		}
		return pollNoError, false
	case coroPollResultClosingV2:
		return pollErrClosing, false
	case coroPollResultTimeoutV2:
		// Close wins over a concurrently delivered timeout, matching Go's
		// pollDesc semantics and preventing a closed descriptor from being
		// surfaced as an ordinary deadline expiry.
		if latomic.LoadUint32(&pd.closing) != 0 {
			return pollErrClosing, false
		}
		// Match Go netpoll's reset-after-expiry rule: a timeout delivered
		// before this waiter runs is stale if its direction now has no
		// deadline or a later future deadline.
		current := pollDeadline(pd, mode)
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
// pollCoroWaitOneV2. The catalog remains the typed lifetime root. Callers
// throughout internal/poll and the standard library keep their Go blocking
// style without a Future/Await API or one worker thread per wait.
//
//go:linkname poll_runtime_pollWait internal/poll.runtime_pollWait
func poll_runtime_pollWait(ctx uintptr, mode int) int {
	for {
		fd, interest, deadline, status := pollCoroPrepareWaitV2(ctx, mode)
		if status != pollNoError {
			return status
		}
		status, retry := pollCoroFinishWaitV2(ctx, mode, pollCoroWaitOneV2(fd, interest, deadline))
		if !retry {
			return status
		}
	}
}

//go:linkname poll_runtime_pollWaitCanceled internal/poll.runtime_pollWaitCanceled
func poll_runtime_pollWaitCanceled(uintptr, int) {
	// Unix does not use this Windows async-I/O cancellation hook.
}

//go:linkname poll_runtime_pollReset internal/poll.runtime_pollReset
func poll_runtime_pollReset(ctx uintptr, mode int) int {
	pd := pollRootGet(ctx)
	if pd == nil {
		return pollErrNotPollable
	}
	if _, ok := pollInterest(mode); !ok {
		return pollErrNotPollable
	}
	if latomic.LoadUint32(&pd.closing) != 0 {
		return pollErrClosing
	}
	if pollDeadlineExpired(pollDeadline(pd, mode)) {
		return pollErrTimeout
	}
	return pollNoError
}

//go:linkname poll_runtime_pollSetDeadline internal/poll.runtime_pollSetDeadline
func poll_runtime_pollSetDeadline(ctx uintptr, delay int64, mode int) {
	pd := pollRootGet(ctx)
	if pd == nil {
		return
	}
	if latomic.LoadUint32(&pd.closing) != 0 {
		return
	}
	deadline := pollAbsoluteDeadline(delay)
	switch mode {
	case 'r':
		latomic.StoreInt64(&pd.rd, deadline)
		llgoCoroPollUpdateDeadlineOrAbortV1(pd.fd, coroPollInterestReadV2, deadline)
	case 'w':
		latomic.StoreInt64(&pd.wd, deadline)
		llgoCoroPollUpdateDeadlineOrAbortV1(pd.fd, coroPollInterestWriteV2, deadline)
	case 'r' + 'w':
		latomic.StoreInt64(&pd.rd, deadline)
		latomic.StoreInt64(&pd.wd, deadline)
		llgoCoroPollUpdateDeadlineOrAbortV1(pd.fd, coroPollInterestReadV2, deadline)
		llgoCoroPollUpdateDeadlineOrAbortV1(pd.fd, coroPollInterestWriteV2, deadline)
	}
}

//go:linkname poll_runtime_pollUnblock internal/poll.runtime_pollUnblock
func poll_runtime_pollUnblock(ctx uintptr) {
	pd := pollRootGet(ctx)
	if pd == nil {
		return
	}
	if latomic.LoadUint32(&pd.closing) != 0 {
		return
	}
	latomic.StoreUint32(&pd.closing, 1)
	llgoCoroPollPostClosingOrAbortV1(pd.fd, coroPollInterestReadV2)
	llgoCoroPollPostClosingOrAbortV1(pd.fd, coroPollInterestWriteV2)
}

//go:linkname poll_runtime_isPollServerDescriptor internal/poll.runtime_isPollServerDescriptor
func poll_runtime_isPollServerDescriptor(fd uintptr) bool {
	return llrt.CoroNativePollServerDescriptorV1(fd)
}

func nanotime() int64 { return runtimeNano() }
