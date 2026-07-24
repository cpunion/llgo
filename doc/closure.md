# Closure and Function-Value ABI

This document defines LLGo's closure-context ABI. It intentionally separates
how a context reaches a callee from the in-memory layout of a Go function
value.

## Scope

The closure-context migration itself has one narrow purpose:

- keep the fixed two-word function value `{code, environment}`;
- pass the compiler-owned `__llgo_ctx` parameter through a target-specific
  hidden context register where LLVM supports one;
- let a plain function and a capturing closure share the same visible call
  syntax and argument ABI;
- stop generating `__llgo_stub.*` merely to adapt a plain function to a
  function value.

It does **not** introduce Go's variable-sized `{code, environment...}` funcval,
change closure allocation or GC scanning, or change coroutine result/handle
representations. Reflection is a separate layer over this ABI. The current
native stackless profile now implements that layer, as described below; it did
not require changing the two-word function value.

## Stable Function-Value Representation

Every Go function value remains:

```text
{ code: unsafe.Pointer, environment: unsafe.Pointer }
```

The representation is exactly two pointers on every target.

- A statically proven plain declared function may be `{address(function),
  nil}` when no managed dynamic dispatch is demanded.
- A raw compatible function pointer is `{pointer, nil}`.
- A capturing closure is `{address(closure body), context allocation}`.
- A method value or interface method value stores its receiver/context in the
  environment word.
- A value which can reach managed dynamic dispatch stores a descriptor in the
  code word, but still uses the same two-word carrier.

The environment allocation remains a fixed typed object known when the closure
is built. This preserves the existing GC and escape-analysis contract.

## Semantic Context Parameter

Capturing function bodies retain a compiler-injected parameter named
`__llgo_ctx`. It remains present in LLVM IR because LLVM parameter attributes
describe which value is transported in the target's context register.

For a normal closure body the parameter is first:

```llvm
define i64 @closure(ptr nest %ctx, i64 %arg)
```

A physical coroutine entry can prefix its own parameters:

```llvm
define ptr @closure.coro(ptr %g, ptr %out, ptr nest %ctx, i64 %arg)
```

Only the parameter named `__llgo_ctx` receives the closure-context attribute.
The method receiver, `g`, result slot, coroutine handle, and other
compiler-owned parameters remain ordinary ABI parameters.

Calls through a function value always supply the environment value as this
semantic parameter. On a hidden-context target it occupies the dedicated
register and does not shift visible arguments. A plain callee has no
`__llgo_ctx` formal and simply ignores that register, so its real code address
can be stored directly in the function value.

## Target Policy

There is one compiler policy that selects the transport:

| Target | LLVM | Transport |
| --- | --- | --- |
| x86 / x86-64 | 19–22 | `nest` |
| ARM | 19–22 | `nest` |
| RISC-V 32/64 | 19–22 | `nest` |
| AArch64 Darwin/Windows/Android | 19–20 | `swiftself` |
| other AArch64 | 19–20 | `nest` |
| all AArch64 | 21–22 | `nest` |
| WebAssembly and unsupported backends | 19–22 | explicit parameter fallback |

LLVM 19–20 assign AArch64 `nest` to X18, which is reserved on the affected
platforms. `swiftself` uses X20 there. LLVM 21 moved AArch64 `nest` to X15 and
also enabled it in the Darwin PCS.

WebAssembly explicitly rejects `nest`. Its `swiftself` attribute does not
provide a hidden register under the C calling convention, so pretending
otherwise would create incompatible indirect-call types. WebAssembly therefore
keeps the old explicit-context adapter until it gains a backend-specific
canonical function ABI.

Unknown and externally supplied embedded backends fail toward the explicit
fallback. A new backend is enabled only after an object/assembly test proves
its context attribute and register mapping.

## Plain Functions and Raw Function Pointers

On a hidden-context target:

```text
plain declaration -> {real symbol, nil}
raw function ptr  -> {raw pointer, nil}
```

No wrapper and no environment cell are allocated. The indirect call carries a
nil context in the hidden register; the ordinary callee's visible ABI is
unchanged.

On an explicit fallback target, `__llgo_stub.*` remains temporarily:

- a declaration adapter ignores the explicit context and calls the real
  symbol;
- a raw-pointer adapter loads the pointer from its environment cell.

This namespace is therefore a portability fallback, not part of the native
function-value ABI. Runtime metadata readers may retain compatibility with
legacy stub symbols until all stored artifacts using them are retired.

## ABI-Rewriting Passes

`nest` and `swiftself` are ABI-treatment parameter attributes. Any pass that
rebuilds a function or call signature must preserve and remap them.

LLGo currently has two such passes:

1. target-independent large-aggregate return lowering, which inserts an `sret`
   parameter;
2. target C ABI lowering, which may insert `sret`, remove empty parameters, or
   split aggregate parameters.

Both definition and call-site attributes are remapped from the old semantic
parameter index to the new physical index. The closure context is
pointer-shaped and must never be split or removed. Verification tests cover a
leading context and a context after coroutine-owned parameters.

## Coroutine Integration

The closure environment and coroutine scheduler state are orthogonal:

- `__llgo_ctx` identifies the captured lexical environment;
- `g` identifies the scheduled goroutine/task;
- `out` identifies the result slot;
- the LLVM coroutine frame owns suspension state.

Coroutine descriptor plain entries and coroutine entries expose an ordinary,
portable C ABI:

```text
plain: (env, args...) -> results
coro:  (g, out, env, args...) -> handle
```

Their target-specific descriptor thunk is the final call chunk: it accepts
`env` as an ordinary argument, then marks only its typed call into the selected
Go body with `nest` or `swiftself`. This is the LLGo equivalent of
`ffi_call_go` without requiring libffi to know LLVM's target-specific context
register. It also keeps WebAssembly on the same descriptor contract; its thunk
uses the explicit-context fallback internally.

LLVM coroutine splitting can spill a context value into the coroutine frame
like any other live value; resume and destroy entries do not require a second
closure-context convention.

Descriptor thunks have their own versioned dispatch contract and are not
removed merely because `__llgo_stub` disappears. They can be simplified
separately after descriptor ABI tests prove direct entries equivalent.

## Reflection Boundary

The native implementation of `reflect.Value.Call`, `CallSlice`, and
`MakeFunc` uses the same fixed `{descriptor, environment}` carrier:

1. `Value.Call` builds ordinary target-C-ABI libffi signatures for
   `(env,args...)->results` and `(g,out,env,args...)->handle`.
2. `runtime/internal/ffi.CallLLGo` validates the descriptor and selects its
   plain or coroutine capability.
3. For a coroutine entry, the compiler-owned `llgo.coroFFICall` intrinsic
   invokes stock `ffi_call` only long enough to create and initially suspend
   the child. It then awaits that child after the libffi frame has returned.
4. The descriptor's final typed thunk moves the ordinary `env` argument into
   `nest`/`swiftself` (or the explicit fallback) for the real Go body.

This is the intended `ffi_call_go`-equivalent behavior: libffi classifies only
visible arguments/results and never needs to know the target closure-context
register. No native/libffi stack frame survives a Go suspension.

`MakeFunc` uses the reverse form. A libffi closure callback performs a bounded
copy from libffi-owned arguments into managed storage and invokes one
compiler-published coroutine ramp until it returns the initially suspended
child handle. The managed ramp calls the user function and writes results
after any number of suspensions. Bound reflective method values reuse this
bridge; direct reflective method calls and `Type.Method.Func` use the
descriptor stored in `Method.Tfn_` with an explicit receiver.

Result slots follow LLGo's physical LLVM aggregate layout, not libffi's
one-byte placeholder for an empty struct. Tests cover zero-sized and
high-alignment zero-sized results, multiple/aggregate results, variadics,
captured function-valued arguments, methods, interfaces, panic/recover, and a
timer suspension.

This implementation is currently a native libffi capability. WebAssembly,
Harvard/bare-metal targets, and environments without executable memory still
need compiler-generated signature trampolines (or another target capability)
for `MakeFunc`; they must not silently reuse the native closure allocator.
Runtime-created signatures which were not reachable at link time remain an
explicit AOT capability boundary.

## Acceptance Gates

The migration is complete only when all of these remain true:

- function values are exactly two pointers;
- native plain function values contain the real code address and no
  `__llgo_stub`;
- native raw function pointers require no allocation or wrapper;
- closure definitions and indirect calls carry the selected attribute at the
  exact physical context index;
- large-return and C ABI lowering preserve that attribute;
- native `Value.Call/CallSlice`, `MakeFunc`, concrete/interface method values,
  variadics, aggregate/zero-sized results, and a suspended timer call pass
  through the descriptor boundary without retaining a libffi frame;
- WebAssembly continues to verify and run through the explicit fallback;
- closure, method value, interface method, defer, goroutine, channel/select,
  and coroutine dispatch tests pass;
- `test/*` and the selected Go standard-library/GOROOT compatibility suites
  show no regression.
