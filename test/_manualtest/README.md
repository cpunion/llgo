# Manual caller-info acceptance playground

Each directory runs under both `go` and `llgo` for side-by-side comparison.
Every scenario also has an automated regression in `test/go`
(`caller_acceptance_test.go`); this playground exists for eyeballing real
output. From the repository root:

    export LLGO_ROOT=$(git rev-parse --show-toplevel)
    go build -o /tmp/llgo ./cmd/llgo    # or: go run ./cmd/llgo ...

Conformance bar: output format and user-code file:line match gc exactly;
runtime-internal, patched-stdlib and startup frames may differ.

## panic — Go-style traceback for an unrecovered panic
    cd test/_manualtest/panic
    go run .            # gc's goroutine traceback
    /tmp/llgo run .     # same shape: names + file:line + offset (exit 2)

## logging — log.Lshortfile / slog AddSource
    cd test/_manualtest/logging
    go run . && /tmp/llgo run .    # the main.go:NN locations must agree

## callers — Caller ladder / CallersFrames / FuncForPC panorama
    cd test/_manualtest/callers
    go run . > /tmp/gc.txt; /tmp/llgo run . > /tmp/llgo.txt
    diff /tmp/gc.txt /tmp/llgo.txt
    # line columns must agree; known diffs: runtime-internal frame lines,
    # gc's runtime.main/goexit tail frames

## testfail — llgo test failure locations
    cd test/_manualtest/testfail
    go test .           # x_test.go:NN: boom
    /tmp/llgo test .    # the same x_test.go:NN: boom

## cexcept — hardware faults in C code called from Go
    cd test/_manualtest/cexcept
    /tmp/llgo run . segv recover     # NULL store in C: recover works, prints
                                     # the post-recover stack
    /tmp/llgo run . segv norecover   # unrecovered: panic + gc-style traceback
    /tmp/llgo run . div recover      # arm64 integer division does not trap
                                     # (hardware returns 0); amd64 raises
                                     # SIGFPE

Verified for the stackless worker path on darwin/arm64 (Linux execution remains
a CI gate):
- SIGSEGV/SIGBUS in a callback-free C worker call become Go panics with the
  standard runtime error text. Three consecutive recoveries succeed, and both
  recovered `debug.Stack()` and an unrecovered terminal traceback contain the
  faulting C identity, compiler-known source C entry, active stackless Go frame,
  and its logical Go callers. SIGFPE uses the same result route on architectures
  whose integer division traps.
- The signal handler records only the signal kind and ucontext PC in worker TLS,
  then returns through `siglongjmp`. The owning executor validates the scalar
  result and constructs the Go snapshot from the active LLVM-frame descriptor;
  neither a worker-side Go stack walk nor function-address reverse lookup is
  used.
- Known limitations:
  1. Exact ucontext PC extraction currently covers Darwin/Linux arm64 and
     x86-64. Other native architectures compile but fail closed if a worker
     hardware fault cannot provide a nonzero PC.
  2. A managed C-to-Go reentry cannot be abandoned by `siglongjmp`; that exact
     same-M boundary disables the landing pad and retains the process signal
     disposition.
  3. On Linux, C symbol names still depend on what `dladdr` exposes. Missing
     dynamic names are printed as `unknown`; the runtime does not guess a
     neighboring Go symbol from address order.
  4. No sigaltstack: on stack overflow the handler cannot run and the
     process dies (gc prints "stack overflow"). Note that C-side UB gets
     propagated by clang (this test was once optimized into infinite
     recursion); wrap/fault.c uses a volatile pointer to prevent that.
