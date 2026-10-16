# WASI Preview 1 filesystem adapter

This target uses the existing coroutine HostOp source, completion, cancellation
and bounded reactor queue. It does not add a scheduler, executor, worker pool,
or an effect-analysis exception. Ordinary Go functions infer their effects.

## Boundary and ownership

Go 1.27 declares 25 distinct fd/path imports in `syscall/fs_wasip1.go`.
`internal/syscall/unix` duplicates nine path imports in its `at_wasip1.go` and
`utimes_wasip1.go` implementations. These lower boundaries, not the public Go
methods, are the adaptation point:

| Surface | Handling |
| --- | --- |
| `fd_read`, `fd_write`, `fd_pread`, `fd_pwrite` | Shared pending source; readiness, short I/O, retry and cancellation |
| `fd_close`, `fd_seek`, `fd_readdir` | HostOp; Close retires pending transfers before fd reuse |
| `fd_filestat_get`, `fd_filestat_set_size`, `fd_sync`, `fd_fdstat_set_rights` | HostOp |
| `path_open`, `path_filestat_get`, `path_filestat_set_times` | HostOp with exact fd-relative WASI arguments |
| `path_create_directory`, `path_unlink_file`, `path_remove_directory` | HostOp |
| `path_rename`, `path_link`, `path_symlink`, `path_readlink` | HostOp |
| `fd_fdstat_get`, `fd_fdstat_set_flags`, `fd_prestat_get`, `fd_prestat_dir_name` | Existing narrowly trusted startup/descriptor metadata imports |

The nine `internal/syscall/unix` declarations alias the defined syscall
adapters. The standard library retains `preparePath`, the Go `cwd`, preopen
selection, `Openat`, flags/rights policy, timestamp policy, and directory entry
decoding. WASI no longer selects the eight public file wrappers used by the
other WebAssembly targets. In particular, a successful Go `Chdir` cannot be
followed by a libc `open` that consults a different working directory.

The C reactor invokes raw path imports with the supplied pointer **and length**.
The public wasi-libc path helpers are unsuitable here: they calculate `strlen`
instead. No NUL-terminated copy buffer, pathname-size limit, libc path resolver,
or duplicate Go-to-POSIX flag conversion is needed in the reactor.

`random_get`, clocks and process termination are separate capabilities, not
filesystem operations. WASI's `proc_exit` is a terminal leaf; it does not create
a pending HostOp. Other core WebAssembly embeddings still lack this process
service and must not silently claim successful termination.

## Wire ABI and lifetimes

Class 3 is the WASI-profile-specific catalog of 21 raw fd/path operations.
The two generic class 1 buffer read/write operations remain the live interface
of `internal/poll`'s deadline-aware adapter, on WASI as on other embeddings.
Both enter the same pending queue, readiness and cancellation machinery; class 1
supplies a single buffer, and class 3 supplies WASI iovecs and output pointers.
There is no restored class 1 open/path catalog or second cwd resolver. The runtime
treats the operation codes as opaque; it does not reinterpret one ABI as another.

The existing transport has nine argument words and three result words. The
WASI adapter returns `errno` through the third result; count/stat/fd outputs use
the standard import's borrowed output pointers. Each Go pointer operand is
passed as `unsafe.Pointer`, so compiler-owned frame roots retain it until the
exact completion or cancellation acknowledgement. Standard iovec callers keep
the backing Go slices alive with their existing `runtime.KeepAlive` calls.

On wasm32, 64-bit scalars use explicit low/high words. `path_open` carries its
two 64-bit rights sets in one borrowed `[2]uint64` record, keeping its operation
within eight words. The reactor validates the exact opcode, arity and 32-bit
word range before interpreting an operation; it bounds-checks that rights
record and copies it without assuming pointer alignment. The small local record
uses ordinary compiler-managed Go storage; the transport adds no separate
allocation or pointer-to-function reverse lookup mechanism.

## Progress and cancellation limits

Native regular-file operations can be offloaded from scheduler execution to
workers. **WASI Preview 1 imports are synchronous host calls.** Moving a call
to the host turn, polling readiness, or setting `NONBLOCK` does not prove that
disk I/O or filesystem metadata will not block inside the host. Hard progress
requires an asynchronous host API or an embedding able to offload the import;
the Preview 1 adapter does not claim that guarantee.

Inherited streams may have read/write rights but no right to change fd flags.
The reactor checks that capability before setting `NONBLOCK`, then requires
normal readiness. If the host cannot poll a stream and it is not already
nonblocking, the operation completes with `NOTSUP`; it does not fall through
to an unbounded synchronous read. Regular files retain Preview 1's explicit
synchronous-host limitation. A real fd metadata failure is never swallowed.

Queued data transfers share the existing exact source-generation obligation.
Cancellation before service avoids the transfer; after a synchronous import
starts, Preview 1 cannot interrupt it. Close completes pending transfers with
`BADF` before descriptor reuse. Host cancellation/completion races retain the
common source protocol, including borrowed-buffer lifetime acknowledgement.

## Gates

- Real production C reactor in Wasmtime: cancellation, positional cursor
  preservation, short reads, close retirement, permission errors, metadata
  failure propagation, inherited stdout and unsupported-stream fallback.
- Production Go adapter SSA shapes: all 21 constant opcodes, argument arities,
  typed pointer masks, and the eight-/nine-word maximum cases.
- Source overlay: the standard public Go bodies remain, raw blocking imports
  are replaced, and all nine unix declarations use the shared adapters.
- Actual LLGo programs: positional I/O; Go cwd/Openat/stat/directory/namespace/
  timestamps; standard stdout; `syscall.Exit(42)` and `os.Exit(42)`.

Host and source gates are not a claim that all linked-program gates have run.
See the dated acceptance ledger for completed compiler/target runs. The
separate [JavaScript filesystem adapter](coro-javascript-filesystem.md) preserves
Go's JS-specific bookkeeping; the in-memory wasm-unknown fixture adapter alone
is not production JS filesystem support.

## Reflection boundary

Ordinary `fmt` use retains some `reflect.Value` method metadata. That must not
make a freestanding module import native `ffi_call`. The existing target-selected
FFI implementation exposes one compile-time capability: native targets support
dynamic calls; freestanding targets do not. The managed dispatcher rejects the
unsupported case before its compiler-owned FFI stack cut, so normal SSA constant
folding removes that native call path. There is no empty FFI implementation or
unknown-import allowance.

`reflect.Call` and `reflect.MakeFunc` remain unsupported on WebAssembly and
baremetal until a target-owned dynamic ABI implementation exists. Both fail
explicitly; a negative executable gate catches their panics and requires a real
stdout completion marker. Successful formatting and filesystem tests do not
establish full Go reflection compatibility.
