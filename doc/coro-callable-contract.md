# LLGo coroutine callable 与调用点契约

状态：设计与实现收敛契约

更新：2026-07-26

关联文档：

- [`coro-ir-design.md`](./coro-ir-design.md)：effect、FuncRep、Demand、CoroOverlay 与 OperationRecipe；
- [`coro-async-core-contract.md`](./coro-async-core-contract.md)：scheduler、executor、event source、select、取消与平台 adapter；
- [`llvm-coro-runtime-design.md`](./llvm-coro-runtime-design.md)：LLVM coroutine 物理 ABI、runtime 当前实现与阶段验收。

## 1. 结论

LLGo 需要把三个目前容易混合的问题拆开：

1. **一个目标可以怎样被调用**，由不可变的 `CallableFact` 描述；
2. **某个精确调用点掌握了什么额外上下文**，由 `InvocationFact` 描述；
3. **该调用在当前 target 上如何等待、完成、取消和回收**，由 `OperationRecipe` 描述。

`CallableFact` 不等于函数地址，`InvocationFact` 不等于全局函数属性，
`OperationRecipe` 也不等于新的函数版本。静态 managed 调用仍遵循一个 source
function 一个 primary body；只有函数值进入开放动态边界时才物化 descriptor 或薄
adapter。

标准库和 runtime 使用两级策略：

- 公开 `syscall.XXX` 或普通 foreign callable 提供保守的默认 contract；对可能阻塞的
  调用，默认 contract 必须保持 `MayBlock`，`Auto` 只能选择该 contract；
- `internal/poll`、runtime 或 target adapter 在精确封装边界提供可审计的
  `TrustedInline` refinement，例如证明一次 `read` 使用仍受保护的 nonblocking、
  pollable FD；target refinement只影响这一条静态调用边，不能自动把整个 wrapper
  宣称为不会挂起。若使用当前v1的小wrapper授权，wrapper还必须独立声明
  `executor-safe`，其完整Go body仍由SSA固定点复核。

地址反查被明确禁止。`FuncPCABI0` 的兼容路径只能在地址形成时前向携带一个
compiler-only callable shadow；一旦 provenance 丢失，就必须 fail closed。需要跨
storage、archive 或开放动态边界传递的 foreign callable 使用显式、版本化 descriptor，
不能在消费端从 `uintptr`、pclntab、符号名或运行时表重新猜测语义。

## 2. 目标

本契约的目标是：

- 保持 Go 标准库的同步调用风格和公开函数签名；
- 让 `syscall.XXX`、C declaration、`FuncPCABI0` carrier 和普通 Go wrapper 统一进入
  effect 固定点及 coroutine lowering；
- 将大批逐 C trampoline 的 `workeraddr` 标注收敛到少量 callable contract、调用点
  refinement 和平台 recipe；
- 让同一个 `syscall.Read` 在用户任意 FD、`internal/poll` nonblocking FD、regular
  file、WASI pollable、host Promise 或 baremetal driver 场景下选择不同物理执行方式；
- 保持 LLVM coroutine 只负责 continuation 保存、恢复和销毁，所有外部等待继续复用
  统一的 `ParkState`、`OperationID`、source、result lease、cancel、detach 和
  quiescence；
- 保持 native、WASM/WASI、RTOS/embedded 和 baremetal 使用同一 compiler/runtime
  语义模型，只替换 target capability 与 producer adapter；
- 对未知 C、未知函数地址、未知 ABI、未知线程亲和和未知 pointer lifetime 一律
  fail closed。

## 3. 非目标

本契约不做以下事情：

- 不把所有 C 函数默认视为可安全换线程执行；
- 不把所有 `read`、`write` 或 syscall 都改成 readiness wait；
- 不使用 `map[uintptr]metadata`、pclntab、`dlsym`、符号字符串、LTO 后反汇编或地址
  比较恢复 callable 语义；
- 不为 timer、read、write、socket、worker、Promise、DMA 或 IRQ 增加各自的 compiler
  opcode；
- 不为每个调用创建 pthread、Future、Promise、Task 或每 G mailbox；
- 不复制完整 Go 函数体来生成 sync/async 双版本；
- 不让 wrapper 的信任标注跳过其余 Go body、循环、panic、cleanup、pollWait 或调用图
  分析；
- 不承诺不存在相应 OS/HAL/host capability 的 target 实现完整文件、网络或进程 API；
- 不以 LLVM 19 以下版本、旧 LLGo 二进制 ABI 或旧 coroutine prototype 为兼容目标。

## 4. 不可协商的硬约束

### 4.1 禁止地址反查

函数地址只是物理调用材料，不是 semantic identity。以下实现全部禁止：

- 在 runtime 或 compiler link phase 建立 `uintptr -> CallableFact` 哈希表；
- 调用前扫描 pclntab、ELF/Mach-O symbol、WASM name section 或动态链接符号；
- 将地址与一组已知 C 函数地址逐个比较；
- 根据 trampoline 的显示名或地址所在 section 猜测 blocking、errno、ABI 或 affinity；
- 接受来自用户、host、C global 或不可信 memory 的裸 `uintptr`，再赋予 worker、inline
  或 callback capability；
- 把 link-time symbol collision check 当成 callable contract 来源。

允许的唯一方向是：

```text
exact Go/C declaration
    -> frozen CallableFactID
    -> FuncPCABI0/CallableOf 前向产生 code value + shadow/descriptor
    -> exact consumer 验证并消费同一个 fact
```

前向 shadow 在编译期 side table 中传播，不改变 Go `uintptr` 的可见布局。它不是运行时
tag，也不是从整数反推事实。

### 4.2 一个 source function 一个 primary body

- `NoSuspend` Go 函数只有 plain primary；
- `MaySuspend` 或需要 suspendable preemption 的 Go 函数只有 coroutine primary；
- 静态 caller 根据 callee effect 使用 plain direct call 或 structured await；
- 动态 callable 只物化 descriptor、entry slot和必要的薄 adapter，不复制 source CFG；
- `Auto` 与 `TrustedInline` 是同一个 callable 的 contract 选择，不是两个完整函数版本；
- C declaration 没有 Go source body，允许有多个固定 ABI adapter，但 adapter 只能做参数/
  结果封送和协议切换，不能复制 managed source 逻辑。

### 4.3 未证明即 fail closed

以下任一事实未知时，不允许静默 inline 或送到通用 worker：

- exact target 或闭合 dynamic candidate set；
- structural ABI、arity、word width、calling convention 或 errno/result convention；
- worker thread safety、thread/realm affinity；
- managed callback/reentry；
- Go pointer retention、pin/root/copy lifetime；
- blocking/cost bound；
- completion、cancel acknowledgement 或 physical quiescence路径；
- target 对所选 backend 的 capability。

失败可以是编译诊断、链接诊断或明确的 target `Unsupported`，不能退化成阻塞唯一
executor、伪造完成或提前销毁 frame。

## 5. 三层事实模型

### 5.1 `CallableFact`

`CallableFact` 是一个 exact callable 的不可变 producer fact。概念结构如下；字段名是
设计 schema，不提前冻结 Go struct layout：

```go
type CallableFact struct {
    ID              CallableFactID
    Origin          CallableOrigin       // GoDefined / CDeclared / HostImport / Assembly
    Identity        CallableIdentity     // FunctionID 或 C link identity，不是地址
    ABI             CallableABI
    Default         CallableContract
    TrustedInline   *CallableContract
    Descriptor      DescriptorPolicy
    ContractDigest  Digest
}

type CallableContract struct {
    Blocking    BlockingPolicy
    Cost        CostPolicy
    Mobility    MobilityPolicy
    Affinity    AffinityPolicy
    Reentry     ReentryPolicy
    Arguments   ArgumentLifetimePolicy
    Results     ResultConvention
    Failure     FailureConvention
    BackendCaps RuntimeCapabilitySet
}
```

每个可从公开或未知上下文调用的 foreign callable 都必须有 `Default`。没有更强、无条件
证据时，`Default.Blocking` 从 `MayBlock` 开始；它不能因为同时存在
`TrustedInline` 而被解释为 inline-safe。

`TrustedInline` 是可选的 executor-safe refinement。它可以：

- 在额外上下文证明下把一次调用收敛为 `BoundedNoBlock`；
- 缩短 argument/result retention lifetime；
- 将可执行域收窄到更具体的 owner/realm；
- 使用更精确但仍 ABI 等价的 failure/result contract。

它不能：

- 扩大 managed reentry/callback 权限；
- 扩大 pointer retention 或让未root/unpinned pointer跨挂起；
- 把 owner-affine callable 放宽为任意 worker；
- 改变 structural ABI、目标身份或可观察 syscall 语义；
- 由调用点自行制造。它必须已经属于同一个冻结的 `CallableFact`。

Go defined callable 和 foreign callable 使用相同 schema，但事实来源不同，见第 6 节。

### 5.2 `InvocationFact`

`InvocationFact` 绑定一个 exact source/emission site 与一个 callable fact：

```go
type InvocationFact struct {
    Site          EmissionSiteID
    Callable      CallableFactID
    Policy        InvocationPolicy     // Auto / TrustedInline
    ContextProof  ContextProofID
    FunctionArg   *ExactFunctionArgumentUse
    ArgumentMap   ArgumentMap
    Keepalives    []ValueRoot
    CandidateJoin CandidateContractJoin
    Digest        Digest
}
```

`InvocationFact` 属于调用边，不属于 callee 全局属性。它必须冻结：

- caller FunctionID、body artifact和instruction/site identity；
- exact callee或闭合 candidate set；
- 如果 callable 来自函数参数，冻结 `(call, argument-index)` 与 sole-consumer flow；
- target、GOOS/GOARCH、pointer width、ABI和所依赖 runtime capability；
- context proof、nonblocking lease要求、keepalive/pin要求；
- 对应 semantic/physical lowering recipe。

任何 body、alias、call target、platform contract 或 proof依赖变化都会使 digest变化并重新
分析。

### 5.3 `OperationRecipe`

`OperationRecipe` 只在调用确实需要 stack cut 或外部 source 时使用。它复用
`coro-ir-design.md` 中的封闭 protocol family：

```text
DirectWait
RegisteredEventWait
ForeignWait
HostWait
WaitSetSelect
```

概念结构为：

```go
type OperationRecipe struct {
    ID          OperationRecipeID
    Family      ProtocolFamily
    Capability  RuntimeCapability
    Bindings    ProtocolPrimitiveBindings
    Commit      CommitModel
    Completion  EarlyCompletionPolicy
    Cancel      CancelPolicy
    Result      ResultLeasePolicy
    Quiescence  QuiescencePolicy
    Affinity    AffinityPolicy
    Admission   AdmissionPolicy
    Slots       []SlotRequirement
    Outcomes    []OutcomeMapping
}
```

`CallableFact` 回答“目标允许怎样调用”，`InvocationFact` 回答“本调用点选择哪个经过证明
的 contract”，`OperationRecipe` 回答“当前 target 怎样执行并等待”。三者都不能由
一个 `PollRead | WorkerRead | HostRead | IRQRead` 枚举替代。

## 6. Contract 的来源

### 6.1 Go body 的推导

对有精确 SSA body 的 Go 函数，compiler可以推导：

- managed direct/defer/dynamic call graph的 effect固定点；
- `NoSuspend`、`MayPark`、`WaitPlatform`、`WaitHost`、`WaitForeign`；
- preemption、cleanup、panic/outcome需求；
- function value是否 direct、是否需要 descriptor；
- exact ABI、参数/结果类型以及显式 keepalive候选；
- body内是否存在未知 foreign、assembly、callback、循环或递归SCC。

推导不能凭 Go body证明外部环境事实，例如：

- 某个整数 FD 当前仍是 `O_NONBLOCK`；
- 某种设备 readiness是否可靠；
- host import会否同步回调或长时间占用host线程；
- C函数会否保存传入pointer；
- syscall在特定内核、filesystem或driver上是否可能长期等待。

这些事实必须来自显式 contract或wrapper持有的runtime invariant。

### 6.2 C、assembly 与 host callable

bodyless C declaration、assembly和host import没有可供Go effect分析的完整body。其
`CallableFact` 只能来自以下可信来源：

1. frontend冻结的 declaration metadata；
2. runtime/stdlib随target提供的版本化 contract catalog；
3. 生成器从同一份目标 ABI 描述产生的 declaration与fact；
4. 可验证的LLVM/assembly footprint加显式semantic contract。

一个 C contract至少需要说明：

- exact link identity、physical symbol和structural ABI；
- 默认是否可能阻塞或长时间运行；
- 是否可在任意worker thread调用；
- owner thread、host realm、TLS、signal mask或IRQ affinity；
- 是否可能callback/re-enter managed Go；
- 参数是否只按值读取、是否在返回后保留pointer；
- result与errno/GetLastError/NULL/word/int32 failure convention；
- 是否存在可选的 `TrustedInline` refinement以及它所要求的proof。

C签名本身不能证明上述语义。`read(int, void*, size_t)` 的类型无法说明fd类别，
`pthread_*` 的类型无法说明owner关系，普通函数指针也无法说明callback或retention。

### 6.3 Contract 的冻结和跨包传播

producer archive summary至少携带：

- `CallableFactID`、origin、identity和contract版本；
- Default/TrustedInline contract digest；
- ABI、target capability和descriptor schema；
- exported/address-taken callable的effect、FuncRep和entry需求；
- context proof与consumer recipe依赖。

注释中的 `foreign.v1` 是输入schema版本，不是所有foreign行为共享的契约身份。冻结阶段
必须按schema及四维行为生成content-addressed `ContractID`；不同行为不得因为使用同一条
注释语法而在catalog中共用ID。

import和link只能验证、join或拒绝这些事实，不能根据最终地址补齐遗漏的contract。

### 6.4 当前 `foreign.v1` 输入语法

当前frontend接受一条exact `ast.FuncDecl` 上的单一指令：

```go
//llgo:coro contract foreign.v1 \
//  progress=may-block affinity=any-thread reentry=none memory=borrow-until-return \
//  inline-progress=executor-safe inline-affinity=any-thread \
//  inline-reentry=none inline-memory=borrow-until-return
//go:linkname read C.read
func read(fd int32, p unsafe.Pointer, n uintptr) int32
```

实际指令必须写在同一注释行；上例换行仅为展示。四个默认字段必填，四个
`inline-*` 字段必须全部出现或全部省略。可选 `scope=declaration|wrapper` 必须与
是否存在Go body一致；可选 `abi=<stable-token>` 用于显式抽象ABI，否则从冻结的typed
ABI生成。`worker`、`poll`、`host`等backend词不能写入通用contract。

v1精确调用点producer采用一个刻意较窄的信任边界：

```go
//llgo:coro contract foreign.v1 scope=wrapper \
//  progress=executor-safe affinity=caller-thread reentry=none memory=borrow-until-return
func nonblockingReadAttempt(...) ... { return read(...) }
```

只有该wrapper中的ordinary static `*ssa.Call`，且target默认是
`unknown`/`may-block`/`async-completion`之一、target自身拥有ABI相同的executor-safe
refinement、refinement不要求当前direct path尚未实现的affinity/reentry/memory adapter时，
frontend才签发`TrustedInline`调用点certificate。Default可以保守投影为
`ThreadAffine|OpaqueExec`；graph只在这一exact edge上用selected projection替换它，并继续
传播独立的`IRQUnsafe`、`MayUnwind`等执行约束。certificate
绑定caller/target certificate、精确block与semantic instruction ordinal、target identity、
contract和ABI。普通caller、动态调用、错ABI、错contract和仅有地址的调用保持`Auto`。
复杂的`internal/poll.FD.Read`不应整体标成executor-safe；它应调用这种小attempt wrapper，
或等待后续region/context proof与NonblockingLease接线。

## 7. Wrapper 信任边界

### 7.1 精确边与完整wrapper复核

标准库wrapper通常同时包含快速尝试、EINTR循环、EAGAIN处理、pollWait、deadline、
close和cleanup。可信信息只应覆盖其中一个exact inner invocation：

```text
FD.Read
  -> exact syscall.Read invocation may use TrustedInline
  -> EAGAIN branch仍调用 pollWait并传播MayPark
  -> deadline/close/cancel仍进入普通operation协议
```

不能把 `FD.Read` 自身标成 executor-safe，也不能因为内部一个 `read` 安全而忽略：

- `pollWait`；
- read/write lock和semaphore slow path；
- EINTR loop的preemption成本；
- defer、panic、result reconciliation；
- regular-file fallback；
- 其他未经认证的foreign call。

### 7.2 `ignoringEINTRIO(extraInfo, syscall.Read)`

在 `internal/poll` 一类wrapper中显式传递调用上下文是推荐方向：

```go
// 仅示意，不冻结源码API。
n, err := ignoringEINTRIO(info, syscall.Read, fd.Sysfd, p)
```

但 `extraInfo` 不能只是普通runtime参数。当前effect计划以函数为单位；若共享helper的
间接调用同时接收到可能挂起和可信inline目标，普通上下文无关分析会把所有effect合并，
导致整个helper保持 `MaySuspend`，或者在错误放宽时造成unsound inline。

推荐将这一形状实现为一个通用 contextual-call recipe：

1. frontend识别exact helper contract，而不是函数显示名；
2. 证明函数参数是闭合、非nil、单一target，且只在该exact argument use被消费；
3. compiler在caller site展开EINTR retry和所选invocation；
4. `Auto` 分支可产生 `ForeignWait`，`TrustedInline` 分支直接调用；
5. loop backedge仍计入preemption budget；
6. helper不产生runtime对象或新的动态function descriptor。

过渡期可以使用两个很小的helper，例如默认版本与nonblocking版本。不得为了避免两个
小wrapper而引入完整context-sensitive effect cloning或复制任意用户函数body。

### 7.3 信任主体

只有 runtime、目标适配层和受控标准库内部包可以产生 `ContextProofID`。普通用户代码
即使构造相同整数、结构值或注释文本，也不能获得proof。proof必须绑定：

- exact wrapper、call site与callee；
- 被证明的operand，例如特定fd和buffer；
- invariant owner与lifetime；
- target/profile；
- body/contract digest。

通过 `any`、interface、heap、map、channel、未知aggregate、C memory或开放archive边界
传递会失去调用点proof。后续动态调用回到 `Auto`。

## 8. `Auto` 与 `TrustedInline`

### 8.1 `Auto`

`Auto` 是公开和动态调用的保守策略：

- 只读取 `CallableFact.Default`；
- 结合target capability选择worker、completion backend、HostOp、owner-affine adapter或
  `Unsupported`；
- 不因为同一fact存在 `TrustedInline` 而自动升级；
- 不因为function address恰好等于某个已知leaf而升级；
- 不因为前一次调用返回很快而缓存inline决定。

例如任意用户调用：

```text
syscall.Read(arbitrary fd)
    -> Auto
    -> native: bounded worker fallback
    -> WASI: target pollable/host policy
    -> JS/WASM: HostOp或Unsupported
    -> baremetal: driver contract或Unsupported
```

确定无条件快速且ABI简单的runtime leaf可以拥有独立、显式验证的inline contract，但
这仍是producer fact，不是consumer猜测。

### 8.2 `TrustedInline`

`TrustedInline` 只能选择同一个fact上已经冻结的refinement，并满足其全部proof：

- exact target与ABI不变；
- invocation处在允许的executor/owner realm；
- required nonblocking/resource lease有效；
- pointer lifetime在直接返回前闭合；
- callback/reentry权限没有扩大；
- cost满足target的inline episode预算，或调用被显式切块。

选择 `TrustedInline` 只表示这一“次尝试”不通过worker/host suspend，不表示wrapper不会
挂起。`EAGAIN -> PollOp`、timer、semaphore、channel和task cancellation仍正常染色。

### 8.3 动态candidate

一个动态callable有多个candidate时：

- `Auto` 对Default contract做保守join；
- `TrustedInline` 要求所有可能candidate都存在相同或可安全join的refinement；
- ABI、affinity、reentry、retention或failure convention不兼容时拒绝；
- 任一candidate只有Default/MayBlock时，不能对整个dynamic call使用TrustedInline；
- open-world candidate set只能使用descriptor的保守auto entry或诊断。

## 9. Nonblocking lease

### 9.1 为什么需要lease

“这个fd曾设置过 `O_NONBLOCK`”不是足够的调用点证明。一次inline `read`至少需要保证：

- fd仍属于同一generation，未close/reuse；
- 在系统调用进入前仍是nonblocking；
- 当前fd类别适合readiness模型；POSIX regular file即使带 `O_NONBLOCK`，实际storage I/O
  仍可能阻塞，不能当作poll-ready socket；
- `SetBlocking`、raw `fcntl` 或host/driver状态变化不能与本次调用形成未同步竞态；
- buffer在直接调用期间有效，且调用返回后不被保存。

因此wrapper提供的不是一个布尔值，而是一个不可伪造、不可逃逸的
`NonblockingLease` proof。它是resource/context lifetime，不是operation result lease。

### 9.2 Lease contract

概念上的lease包含：

```text
resource identity + generation
direction/readiness class
nonblocking state epoch
owner/synchronization domain
exact invocation operand binding
proof contract ID
```

第一版不要求将其物化为公开Go struct。它可以由compiler验证的private wrapper region、
fd owner lock/reference和runtime metadata共同形成。

当前runtime core已提供首个target-neutral、无分配的`NonblockingLeaseGate`：每个gate拥有
`resource + generation + capability`，每次只允许一个有界attempt；owner执行状态变更时先
`BeginChange`封锁新attempt，等待`ChangeQuiesced`，在sealed窗口修改OS/HAL/host状态，再以
`FinishChange`发布新generation，或以`FinishRetire`留下永久tombstone。复制/重复Release不能
释放另一个holder，close/reuse也不能让旧generation重新有效。它不包含fd、poll、worker或
平台分支；当前尚未接入`internal/poll.FD`，因此只是lease核心而不是stdlib read/write完成声明。

硬性规则：

- lease只能在调用前取得，在该次inline尝试返回后释放；
- lease期间不允许普通coroutine suspend；
- 若EINTR retry需要执行preemption poll，必须在poll前释放，并在下一次尝试前重新验证
  lease，不能把resource invariant跨suspend偷偷延长；
- `SetBlocking` 或等价操作必须与active lease同步并推进state epoch；
- close/reuse必须推进generation，旧lease失败；
- raw外部修改fd flags会使runtime无法证明invariant，相应路径必须回到Auto或明确声明
  unsupported contract；
- lease不能存入interface、heap、global或foreign memory，也不能随descriptor传播。

### 9.3 `read` 的规范映射

```text
internal/poll FD.Read
  -> 判断并取得pollable-nonblocking lease
     -> 成功: TrustedInline read attempt
     -> 不可取得: Auto read operation
  -> EINTR: 释放/重验lease后重试，遵守preemption budget
  -> EAGAIN且pollable: 注册readiness OperationID，park，ready后重试
  -> 普通结果: 保持short read、EOF和errno语义
```

`Pread/Pwrite`、regular file、DNS和无法可靠poll的device通常没有该lease；native走worker
或AIO，其他target按capability选择completion/host/driver。

## 10. `FuncPCABI0` 前向shadow

### 10.1 兼容目的

Go标准库在Darwin等平台使用：

```text
FuncPCABI0(libc_xxx_trampoline) -> uintptr fn -> syscall(fn, args...)
```

源码API要求保留该 `uintptr`，但compiler在形成地址时仍看到exact function operand。
因此生成：

```text
physical SSA value: uintptr/code word
compiler shadow:     CallableFactID + ABI + provenance generation
```

shadow只存在于plan/lowering side table，不改变可观察整数值。

### 10.2 允许的shadow flow

第一版只允许能保持exact provenance的flow：

- direct copy、extract、tuple transfer；
- contract验证过的transparent conversion；
- 所有incoming fact相同的Phi；
- 冻结、只读、whole-program可证明唯一写入的global/aggregate slot；
- exact syscall carrier argument；
- compiler识别的显式 `CallableOf`/descriptor construction。

以下操作立即丢失shadow：

- arithmetic、mask、xor、非透明pointer/integer roundtrip；
- 写入未知global、heap、map、interface、channel或C memory；
- 从外部函数、host或raw memory返回；
- open dynamic merge、不同ABI/fact的Phi；
- unknown call、unknown alias或跨archive缺少summary；
- 仅凭最终link address重新出现。

消费 `llgo.syscall*`、worker carrier或foreign adapter时没有exact shadow即拒绝。不得退回
地址查表。

### 10.3 Shadow 与 physical symbol

linker仍可验证：

- shadow声明的physical symbol存在；
- ABI、visibility、arity与目标object一致；
- 两个不同canonical target没有非法占用同一physical symbol。

这些是完整性验证，不是semantic contract来源。多个合法alias若前向解析到同一canonical
fact可以合并；不能因symbol相同而把一个未认证地址提升为已认证callable。

### 10.4 syscall number是正交能力

Linux的固定`__llgo_linux_syscall{3,6}_v1`适配器只声明`word-call.v1/4`或
`word-call.v1/7`调用ABI；适配器地址本身不证明动态trap可以在任意worker thread执行。
ProgramIR在同一worker certificate事务中另行冻结trap policy：

- intrinsic处的exact constant，或经过closed static parameter carrier到达的exact constant，
  才能形成候选；
- final SSA plan只接受当前managed root实际激活的certified incoming edge，未激活wrapper
  不污染安全路径；
- fork/exec/exit、signal/thread/credential/affinity等target-owned syscall constant拒绝进入
  通用worker，动态值、escaped carrier和open incoming同样fail closed；
- raw/plain root仍执行同步legacy-stack body，不消费worker certificate。

trap identity、每条incoming edge及owner集合都进入冻结SitePlan和certificate digest。
consumer仍不得从最终函数地址或syscall数值反查、补造能力。

## 11. 显式 `Callable` 与 descriptor ABI

### 11.1 何时需要显式Callable

前向shadow适合未修改标准库中的短暂 `FuncPCABI0 -> Syscall*` 数据流。以下情况必须使用
显式Callable：

- callable需要跨global/aggregate或archive边界长期保存；
- 多个foreign target形成闭合动态集合；
- callable进入 `any`、interface、reflect或ABI-visible storage；
- host/C callback registry保存调用能力；
- raw地址与contract需要作为一个不可拆 capability传递。

不能先保存裸地址，再在消费端调用 `CallableFromPC(addr)`；这样的API与本契约冲突。
只允许从exact producer前向构造，例如compiler intrinsic意义上的
`CallableOf(exactTarget)`。

### 11.2 Descriptor原则

descriptor使用版本化、目标配置的物理布局，逻辑上至少携带：

```text
version / kind
CallableFactID或冻结catalog index
structural ABI ID
target-defined entry representation
Auto adapter/capability
可选TrustedInline capability
可选closure/host context policy
```

约束如下：

- descriptor由compiler/linker或可信runtime catalog只读构造，用户不能伪造；
- contract与entry一起前向发布，runtime不按entry反查contract；
- native entry可以是code address；WASM可以是table/import index；embedded可以是ROM
  function capability；
- descriptor的contract index只引用当前program冻结catalog，不是可由外部整数索引的
  任意表；
- static call通常不物化descriptor；
- managed Go descriptor继续分离plain/coroutine entry与closure context，一个descriptor
  不代表两份source body；
- foreign descriptor与managed descriptor使用显式kind/version，不能把C function pointer
  当作Go closure；
- C回调Go需要独立ForeignReentry adapter、rooted context和attach-P协议，不能仅设置
  reentry flag后送进普通worker；
- 32-bit/WASM布局使用显式`u32`字段和编译期size/alignment断言，不能依赖host pointer
  layout。

### 11.3 动态调用

动态descriptor调用先验证kind、ABI和所需policy capability：

- managed descriptor按effect选择plain或coroutine entry；
- foreign `Auto`调用进入descriptor绑定的保守adapter/recipe；
- foreign `TrustedInline`调用还要验证exact context proof与lease；
- closed candidate set可以在compile/link时join contract；
- open或ABI不兼容集合只能使用明确的开放auto adapter，或者诊断不支持。

## 12. 执行、event与backend是正交维度

### 12.1 启动方式

```text
InlineTry      当前executor/owner上执行一次有界、不阻塞尝试
OffloadCall    把同步、worker-safe调用提交到外部执行资源
SubmitAsync    提交AIO、Promise、driver或DMA，本调用本身快速返回token
OwnerCall      在固定M/P、host realm或专用affinity owner执行
```

### 12.2 Event/commit语义

```text
ReadyThenTryCommit       readiness只是hint，winner仍需同步尝试/重验
IrreversibleCompletion  publication时副作用已发生，loser只能丢result
Reservable              backend先保留可回滚reservation，winner commit
```

### 12.3 Producer/backend

```text
Native reactor
Worker pool
AIO/completion queue
Host callback/Promise
RTOS notification
IRQ/DMA
```

这三组不能合并：

- host callback既可发布stream readiness，也可发布Promise completion；
- IRQ既可表示UART ready，也可表示DMA已经完成；
- io_uring operation可能已完成，也可能有可取消的queued reservation；
- worker通常发布irreversible completion，但是否可取消排队job是独立事实；
- readiness candidate输了select时没有执行I/O副作用，completion candidate输了时副作用
  仍可能已经发生。

Affinity、memory lifetime、cancel strength、capacity和cost同样独立，不能由backend名字
隐含。

## 13. Select、取消和quiescence

### 13.1 Select contract

所有可挂起invocation进入现有`WaitSetRecord`和operation resolver：

- 每个candidate拥有独立 `OperationID`、generation、commit model和result cell；
- ready fact只提名candidate，不直接resume G；
- winner由owner-P resolver按Go语义选择；
- loser执行其真实cancel/rollback/discard；
- 所有loser detached或形成pointer-free tombstone后，winner才可promote；
- selected result被task abort/shutdown压制时仍显式Discard；
- duplicate/stale/late producer只能命中exact generation或被拒绝。

`readiness`、worker completion、Promise和IRQ不获得各自的select实现。

### 13.2 取消维度

取消不是单枚举，而是分阶段capability：

```text
before-submit rollback
queued cancel-before-start
active best-effort abort
logical cancellation only
physical cancel acknowledgement
late completion required
strong quiescence/join required
```

同一个operation可同时拥有多个阶段。例如worker job可以在reserve后submit前rollback，未来
可以在queue中cancel-before-start；一旦worker已经进入C调用，就只能逻辑取消并等待late
completion。

### 13.3 各类source的取消

- readiness：unregister/close exact interest并等待reactor不再发布旧generation；
- worker：当前submitted调用不可强杀，result晚到后Discard并回收；
- AIO：按backend区分queued cancel、active abort和normal completion race；
- Promise：有AbortController时best-effort abort并等host ack，否则保留late callback；
- IRQ/DMA：停止device、屏蔽/清pending IRQ并同步ISR/DMA ownership；
- owner-affine调用：取消请求只能在owner safepoint处理，不能从另一个线程远程销毁frame。

task abort、executor shutdown、`context.Context`取消和operation cancel仍是不同层级。

### 13.4 Quiescence

operation slot只有在以下条件同时满足后才可复用generation：

- logical disposition已冻结；
- waiter/frame link已detach；
- result已Take或Discard；
- producer admission已seal且所有inflight producer退出；
- backend unregister、callback tail、worker return、thread join或IRQ fence已经提供物理
  quiescence；
- source-specific pointer/root/pin已经释放。

仅设置 `done`、`canceled` 或从ready queue移除G都不足以证明quiescence。

## 14. 平台映射

### 14.1 Native POSIX与其他native OS

推荐映射：

| 场景 | Contract/recipe | Backend |
| --- | --- | --- |
| pollable nonblocking socket/pipe一次尝试 | `TrustedInline + NonblockingLease` | 当前executor inline |
| `EAGAIN`等待 | `RegisteredEventWait + ReadyThenTryCommit` | epoll/kqueue/poll + doorbell |
| regular file/DNS/不可poll C | `Auto + ForeignWait` | AIO优先，bounded worker兜底 |
| IOCP/io_uring完成 | `SubmitAsync + Irreversible/Reservable` | completion queue |
| TLS/pthread/signal/LockOSThread | `OwnerCall` | pinned M/P或专用owner |
| callback/reentry | explicit Callable + ForeignReentry | attach-P/reentry adapter |
| fork/exec/process critical | special owner/runtime protocol | 禁止普通worker推断 |

当前Darwin/Linux opt-in fleet实现启动时选择1–8个真实M/P，使用每route pipe doorbell与POSIX
`poll`、exact `OperationID.Route`和一个共享固定worker pool；program
start/stop/all-peer-join以及TCP同步标准库链已运行。高并发终态仍需替换或补充scalable
reactor/completion backend、运行期P策略和通用steal，但不改变Callable/Invocation schema。

### 14.2 JS/WASM

- 通常一个host M/一个P，`RunSlice`返回host；
- 真正同步、有界且不重入的WASM/host leaf才允许TrustedInline；
- 浏览器文件、网络、timer和其他异步API使用HostWait/Promise，不能阻塞当前JS callback；
- callback必须在later turn发布POD generation fact，不能从Promise continuation直接递归
  resume G；
- Promise取消按API能力选择AbortController或logical cancel + late completion；
- raw native worker默认不可用；启用Wasm threads是独立target profile，不能改变普通WASM
  contract；
- suspendable Go export需要Promise/JSPI/明确的async boundary adapter，不能伪装同步返回。

### 14.3 WASI

- pollable fd、clock订阅使用RegisteredEventWait和`poll_oneoff`/对应preview pollable；
- ready后的`fd_read/fd_write`只有在host contract证明有界时才inline；
- regular file或nonpoll blocking import需要host async、thread/offload capability，否则
  `Unsupported`；
- command/reactor必须消费host action、发布time/ack并调用exact continuation；
- syscall number和POSIX C address不是WASI callable identity。

### 14.4 RTOS/embedded

- nonblocking HAL/MMIO尝试使用TrustedInline，但还需bounded-cost和IRQ-safety证明；
- driver notification、queue readiness使用RegisteredEventWait；
- DMA或异步driver完成使用IrreversibleCompletion/Reservable；
- 没有异步driver的阻塞HAL可使用固定service task pool，但不是每operation创建task；
- executor task/core affinity、ISR入口和SMP route分别建模；
- ISR只携带POD `OperationID`/result，不能分配、加Go锁、访问G/frame或直接resume；
- source、wait、timer、callback和worker/task容量必须静态声明并有确定的exhaustion结果。

### 14.5 Baremetal

- 一个core/一个P main loop使用IRQ ring、hardware compare和WFI/WFE；
- 设备寄存器快速检查可TrustedInline；等待使用IRQ readiness；DMA使用completion；
- 通常不存在pthread worker，`Auto`不能把MayBlock C静默映射为inline；
- 无文件系统、socket或进程能力时，相关标准库API明确Unsupported；
- cancel需要disable source、清pending state并完成IRQ/DMA fence；
- 无lock-free 32-bit atomic的target需要IRQ critical-section atomic adapter，不能使用非原子
  fallback；
- SMP baremetal按core建立P，并用IPI doorbell和稳定route分发operation。

### 14.6 线程亲和、`LockOSThread` 与 foreign session

2026-07-23的真实cgo运行验证确认了一个此前只列为风险的缺口：四个共享worker都能正确
执行单次generated-cgo transaction，但把同一Python runtime的初始化、执行和销毁随机分配到
不同worker会在线程局部解释器状态中崩溃；临时把物理worker数降为1后同一程序通过。这个
实验只定位了通用thread-affinity问题，不授权按Python符号、库名或函数地址增加特例，也不说明
“一个全局worker”是可接受方案。

线程亲和必须作为独立于blocking、event kind和backend的能力建模：

```text
AffinityRequirement
    AnyThread | CallerThread | OwnerThread | HostMain

AffinityLease
    domain ID + generation + nesting + exact owner capability

ExecutionDomain
    native M/thread | host realm | RTOS task/core | baremetal core
```

规则如下：

- `AnyThread`调用继续进入现有共享bounded worker，不携带affinity lease，也不因同一G的前一次
  调用恰好落在某个worker而获得粘性；
- `CallerThread`/`OwnerThread`/`HostMain`必须先取得exact `AffinityLease`。job只前向携带
  domain ID与generation，runtime不能从function address、TLS值或符号反查domain；
- lease在一个logical session内独占其owner，嵌套取得只增加同一G的计数；不同G不能在两个
  相邻foreign call之间插入到同一个session，从而避免“线程相同但session交错”的伪实现；
- worker completion、task cancel和defer cleanup都不得提前释放lease。已进入不可取消C调用时，
  logical cancel仍等待late completion/quiescence，再在owner上完成cleanup与最后一次unlock；
- callback/reentry仍使用独立ForeignReentry/attach-P协议。affinity lease本身不授权C回调Go；
- pool满、owner不可用或target没有相应执行域时，取得lease必须走统一capacity wait或明确
  `Unsupported`，不能退化为占住唯一executor，也不能fail-stop队列；
- 普通wrapper annotation只声明并传播affinity region；完整Go body、内部调用、panic、defer和
  退出边仍分析。只有runtime/受控标准库或target catalog提供的exact contract能授权foreign
  callable，wrapper不能把未知C自行升级为worker-safe或callback-safe。

Native上的Go兼容入口是`runtime.LockOSThread`。它不能继续是空实现：

1. 第一次lock把当前G绑定到一个generation-stable M/execution domain，重复lock只增加嵌套；
2. locked G的runnable continuation、parked result和cleanup只能回到该domain，禁止普通
   P-neutral transfer/steal；
3. 线程亲和foreign call在同一owner上执行。若它可能阻塞，owner必须像Go的M/P模型一样交还或
   补偿可运行P，使其他G继续运行；不能让“保持线程”重新变成“阻塞整个调度器”；
4. 最后一次`UnlockOSThread`只在没有active affine operation、callback或cleanup lease时解除；
5. G异常返回、panic、Goexit和task abort都必须通过compiler cleanup表释放隐式owner资源，但
   不得替用户伪造缺失的`UnlockOSThread`可观察语义。

实现上不应给共享MPMC ring增加一个“lane号然后由任意worker碰运气取走”。可行的最小结构是：

- 保留现有any-thread ring和四个共享consumer；
- target另提供固定容量的`AffinityOwner`目录，每个已租用owner有串行POD ingress；
- compiler/runtime park recipe把同一G的exact lease前向带到提交点，owner只接受匹配的
  `(domain,generation)`；
- owner执行foreign call并通过现有`OperationID`、result lease、cancel/detach/quiescence链返回，
  不新建第二套scheduler或完成状态机；
- 后续动态M/P只扩展owner目录和补偿策略，不改变Callable/Invocation/Operation事实模型。

平台映射为：

| 平台 | affinity domain |
| --- | --- |
| Native | generation-stable M或专用foreign owner；blocking时执行P handoff/补偿 |
| JS/WASM | host-main realm；later-turn continuation保持realm，不制造pthread |
| WASI threads | 显式thread capability；无threads profile为host realm或Unsupported |
| RTOS/embedded | 固定task/core owner，通知队列只携带POD generation |
| Baremetal | 当前core/主循环owner；SMP用core route，单核不可把阻塞C伪装为异步 |

截至2026-07-24，第一版Native闭环已经进入production path：

- `runtime.LockOSThread`/`UnlockOSThread`通过两个exact compiler marker取得/释放当前G对
  executor M/P domain的嵌套逻辑租约；marker执行一次runnable handoff，API返回时该G已重新
  落在同一物理owner；
- scheduler只允许locked G由exact owner选择，禁止普通ready distribution和跨domain迁移；
- ordinary `AnyThread` foreign call仍进入共享bounded worker；locked G的foreign call先查询
  当前task租约，再在当前M直接执行同一个typed `uintptr` thunk，不按库名、符号或地址增加特例；
- 默认四worker配置下的generated-cgo Python init/use/fini已经fresh compile-link-run通过并输出
  `Hello, Python!`；`cgobasic`与`cgodefer`同时通过，证明any-thread worker路径没有被替换；
- compiler marker、runtime owner选择、嵌套lock/unlock、terminal logical lease cleanup和
  architecture-debt gate都有独立测试。

这仍不是完整Go affinity语义。locked G若未unlock就终止，当前会先清除逻辑owner再回收G，
但尚未物理退休/替换被污染的M；一个可能阻塞的locked foreign call会占用该M/P island，现有
第二个native domain可继续推进其他G，但尚无按压力动态创建M/P的补偿机制。WASM、WASI、
embedded和baremetal目前只有目标中立的logical domain模型，没有等价的production affinity
backend。后续完成门仍包括：两个独立session并行、defer/panic/Goexit/cancel清理、错误generation
拒绝、locked-G异常退出的M退休，以及affine C长期阻塞时的动态P补偿。

## 15. 容量、公平性与性能边界

### 15.1 当前fixed worker的边界

当前native profile为4个pthread、1024-slot C11 MPMC sequence ring、最多9个`uintptr`参数和
3个scalar result。多个P可并发预留不同cell，取消已取得的reservation时以tombstone保持FIFO；完成按exact
route投递，并在winner publication前strong-join已admit producer。它验证了stackless
ForeignWait、pointer-free completion、errno capture和late completion，但不是最终高并发策略：

- 4个NFS/FUSE/device/DNS/wait4或其他长期阻塞调用可占满全部worker；
- 后续job即使已经进入1024-slot queue也不会开始；
- 当前submitted job不能从queue移除，也不能终止已经开始的C调用；
- queue满时当前reserve路径仍是fail-stop边界，不是完整的可挂起backpressure；
- shutdown必须等待所有worker返回，永不返回的call会阻止strong join；
- 把短小nonblocking socket syscall也送worker会产生不必要的queue、线程切换和doorbell
  成本。

因此需要：

- generation稳定的capacity source与 `AwaitCapacity`；
- cancel-before-start；
- filesystem、DNS、process wait等阻塞域隔离或有界补偿策略；
- AIO/io_uring/IOCP等completion backend优先承载高并发I/O；
- 永不返回或affine调用使用专用owner/资源，禁止污染公共pool。

无限增加worker或每operation建thread不是解决方案。

### 15.2 当前`poll(2)`边界

当前native poll set最多承载1024个logical Poll operation加1个doorbell。每次wait重建并
扫描固定数组，`poll(2)`本身也是O(N)。它适合作为正确性vertical slice，但：

- 1024是operation数量，不保证等于1024个FD；read/write方向可以分别占槽；
- large idle connection set仍需全量扫描；
- level-triggered hot fd会重复报告ready；
- regular file必须被pollOpen/fstat拒绝，否则“永远ready”会掩盖实际storage阻塞；
- 当前每route reactor与启动期1–8 P fleet demand injection不能外推为运行期P resize、scalable shard、批量/local-deque stealing或完整affinity已经完成。

后续使用epoll/kqueue/IOCP/ready-index ring和dynamic/sharded catalog；这些backend仍发布
相同的OperationID和ReadyThenTryCommit fact，不改变标准库wrapper。

### 15.3 Preemption成本

`NoBlock` 只表示不会等待外部进展，不证明wall-work有界。以下情况仍需cost contract、
切块、compiler poll或offload：

- 大buffer copy/read/write；
- C内部无界或数据相关循环；
- repeated EINTR；
- allocator/loader/locale/DNS等不可见锁；
- compiler-rt、assembly或target intrinsic长路径。

TrustedInline必须同时满足target的episode成本要求。若无法证明，降级为Auto/offload。

## 16. 未知C与通用`Syscall/RawSyscall`

### 16.1 未知C

一个没有CallableFact的C declaration默认同时未知：blocking、worker safety、affinity、
reentry、retention和failure convention。不能因为“多数C调用也许能在线程池运行”就默认
worker。

允许的处理只有：

- 补充精确、版本化C contract；
- 通过受控wrapper转换为已知HostOp/driver/completion操作；
- 在不允许挂起的hard boundary明确同步执行并承认会阻塞整个环境；
- target诊断Unsupported。

### 16.2 命名`syscall.XXX`

命名wrapper可由stdlib/target catalog集中提供CallableFact。例如：

- `Read/Write`默认MayBlock，internal/poll调用点可TrustedInline；
- `Pread/Pwrite/Open/Fsync` native默认worker/AIO；
- `Nanosleep`映射timer而非占用worker；
- `Accept/Connect`使用nonblocking尝试和readiness；
- `Getpid`等无条件小leaf可以有明确inline contract；
- `fork/exec/pthread/signal mask/TLS`使用special/owner-affine contract。

编译器不按函数显示名实现这些语义，而从patch/archive的exact fact读取。

### 16.3 通用`Syscall/RawSyscall`

运行时未知trap/function word还同时隐藏参数pointer schema与affinity。第一版策略：

- static syscall number或FuncPC shadow可专门化为exact CallableFact；
- 闭合、ABI一致candidate set可做保守join；
- dynamic number只有在target提供完整runtime dispatch catalog、参数lifetime contract和
  owner policy时才接受；
- 其他情况fail closed，不能把所有word盲送worker；
- `RawSyscall`与`Syscall`在EINTR、scheduler/owner和errno上的Go语义差异由wrapper
  contract保留，不由worker backend自行改写。

## 17. 与compiler IR的关系

`CallableFact` 属于plan前的semantic facts，`InvocationFact` 在effect/value-flow固定点
期间参与call edge，`OperationRecipe` 在plan后冻结physical protocol：

```text
SSA / declaration metadata
    -> CallableFact catalog
    -> call/value flow + InvocationFact
    -> Effect / Exec / FuncRep / Demand fixed point
    -> CallPlan
    -> Physical OperationRecipe
    -> CoroOverlay control cut / continuation / slots
    -> LLVM emission + verifier
```

规则如下：

- CallableFact不能预先决定某个Go函数是否需要descriptor；
- InvocationFact必须参与effect传播，不能只在emitter临时覆盖；
- Auto的MayBlock调用给caller传播WaitForeign/WaitHost等真实effect；
- TrustedInline消除该exact foreign wait，并以selected contract projection替换同一target的
  Default contract execution projection；wrapper内其余effect和非契约exec约束照常传播；
- contextual helper展开必须登记在LoweringFacts/emission ledger；
- emitter不得重新根据名称、地址或源码pattern选择policy；
- plan digest覆盖fact、invocation、proof、recipe和target capability；
- post-LLVM verifier仍检查真实call、CoroSplit、safepoint、ABI和禁止的未知helper。

## 18. 迁移步骤

迁移不以长期兼容旧prototype为目标，但必须区分生产接线、可运行基础设施和
report-only原型。截至2026-07-22，迁移顺序和状态如下：

1. **通用contract schema与冻结（已落地）**：精确解析 `//llgo:coro contract foreign.v1`，
   区分declaration/wrapper scope，显式表达progress/affinity/reentry/memory。`foreign.v1`
   只是源schema；冻结后的contract identity为
   `foreign.v1/<CallableContractBehaviorDigest>`，不会把不同行为contract混成同一ID。
2. **生产freeze/build/SSA/digest接线（已落地）**：EmissionUniverse为managed-required集合中的
   每个exact C declaration冻结独立、content-addressed的 `CallableIdentityCertificate`，覆盖
   canonical/link identity、callable/typed ABI、physical symbol/ABI、origin和evidence；generic
   contract只是同一identity上的可选behavior certificate。identity和contract通过
   `CoroPlanInput -> SSAFunctionPolicy -> SSAPlan -> CoroPlanDigest` 传递，当前
   `PlanDigestSchema` 当前为 `llgo.coro.plan-digest.v26`。同一physical `(symbol, ABI)`可以对应多个
   exact DeclarationRef；不同declaration的ABI差异不会触发无关的全局冲突。显式generic contract
   必须与其identity certificate逐字段匹配；builder伪造、替换或漏传frontend certificate均失败关闭。
   identity本身不改变execution policy，wrapper metadata也不会屏蔽Go body分析。
3. **宣言语义投影（已落地）**：executor-safe declaration是
   `ExternalKnown/NoSuspend`，may-block/unknown/async-completion保留
   `ExternalUnknownForeign + BlockForeign`，Auto caller获得 `WaitForeign`；no-return另加
   `NoReturn`，但不因此获得executor-safe。affinity/reentry/memory尚无物理adapter的
   维度分别投影为 `ThreadAffine`/`OpaqueExec`以继续fail closed。
4. **target-owned TrustedInline与精确wrapper edge（首个生产闭环已落地）**：同一declaration
   certificate可冻结conservative Default和可选executor-safe refinement；graph/SSA只接受
   target已经拥有、ABI完全一致的refinement。EmissionUniverse只为executor-safe小wrapper中的
   ordinary static `*ssa.Call`生成certificate，绑定caller/target certificate、精确SSA坐标、
   target identity、contract与ABI；CallPlan/current digest和physical resolver再次校验closed
   `DirectPlain`唯一foreign target。graph从target certificate派生Default/selected两个execution
   projection，只替换`ThreadAffine|OpaqueExec`契约lane，不能消除`IRQUnsafe`、`MayUnwind`或其他
   非契约约束；target自身的保守plan保持不变。physical resolver再次核对target确实拥有所选
   contract与ABI，并按同一替换规则验证effective exec。wrapper完整body仍参与固定点并与summary核对。当前v1尚无
   resource NonblockingLease、region proof和cost budget，因此不得将此视为`internal/poll`已可全面inline。
5. **FuncPCABI0 producer-forward shadow（生产授权gate已落地）**：已在exact producer处注入target、
   physical symbol、ABI/arity和certificate identity，并经闭合、private、static `uintptr`
   parameter carrier传到 `llgo.syscall*` sink；它保留每条incoming edge的条件inventory，
   对arithmetic、store/return、export/open/escape、未标注target均失败关闭，且不使用运行时
   `uintptr -> function` 反查。worker syscall freezer直接由forward shadow生成target、owner和
   conditional incoming certificate inventory，不再运行consumer-side reverse provenance。
   仅被取址、且不属于managed-required集合的target目前仍只存在于forward shadow；它尚未扩入
   total callable inventory，这是后续把address-only target纳入统一facts/archive时的独立步骤。
6. **consumer normalization（已落地）**：`FuncPCABI0 + llgo.syscall*`的worker路径只消费
   producer-forward shadow并做whole-plan active incoming校验；重复reverse builder、参数递归和
   第二套carrier/escape索引已删除。`workeraddr`仅保留为producer contract的迁移兼容输入，generic
   `foreign.v1 + abi=word-call.v1/N`优先。
7. **CallableContractFacts投影（已落地，尚未成为唯一archive输入）**：SSAPlan以total identity
   inventory为DeclarationRef全集，把显式generic certificate投影为其contract，把legacy或未标注
   declaration投影为content-addressed unknown behavior；因此managed-required集合内的exact foreign
   invocation不再identity-less。这个unknown仅是facts/catalog语义，不调用generic policy投影，也不
   增加 `ThreadAffine`/`OpaqueExec` 或与legacy冲突。Default/TrustedInline、closed/open Auto join和
   exact invocation site均可canonical化并计算digest；partial candidate、错identity/contract/ABI仍
   失败关闭。v1的InvocationFact尚无独立ContextProof字段，精确proof仍由CoroPlanDigest绑定；跨archive
   schema v2待补。
8. **nonblocking lease与stdlib wrapper（待完成）**：与 `SetBlocking`、close/reuse、
   generation/epoch同步，再以 `internal/poll` read/write/accept/connect作为首批调用点；
   `ignoringEINTRIO(extraInfo, fn)` 之类helper应归一为通用contextual recipe。
9. **descriptor与backend recipe（待完成）**：为escape/dynamic/callback/cross-archive callable
   加入版本化descriptor与runtime catalog；将worker、readiness、Promise、WASI pollable、
   RTOS notification、baremetal IRQ等固化为target-owned `OperationRecipe`，而不把backend名词
   写入通用contract。
10. **并行、平台验收与清理（部分完成）**：native route-aware submission、启动期1–8 M/P target、
    worker lifecycle、P-neutral typed parked-result packet、fleet demand injection/work sharing
    和TCP E2E已落地；仍需AwaitCapacity、queued cancel、运行期P resize、批量/local-deque steal与完整affinity。2026-07-23已用真实generated-cgo/Python序列确认共享worker
    会破坏线程局部session，并冻结第14.6节的`AffinityLease + AffinityOwner`方向；尚未接入
    `LockOSThread`或production owner目录。各target的真实file/socket/timer证据成立后，迁移并
    删除逐trampoline `workeraddr`兼容标注。

## 19. 验证矩阵

本节是最终验收矩阵，不表示每一项已在当前worktree通过。当前已有自动化证据集中在：

- contract parser、四维behavior digest、content-addressed ID、freeze identity/ABI与冲突检查；
- build不能伪造或替换frontend certificate，declaration/wrapper的SSA语义投影；
- total callable identity、generic callable certificate和exact TrustedInline invocation进入
  当前 `PlanDigestSchema v26`，事实变化会改变digest；
- TrustedInline的closed static call、foreign target和physical resolver正/负例；
- producer-forward shadow的direct/private-carrier、条件incoming inventory及
  arithmetic/open/escape/unannotated拒绝用例。

尚未有自动化生产证据的主要区域是resource/context lease、复杂wrapper region proof、
dynamic descriptor、通用backend-recipe catalog、facts archive唯一消费链及非native跨平台E2E。
Native bounded worker、regular-file与双owner TCP链已有生产证据；forward shadow已经是worker
授权gate和最终worker certificate inventory的唯一provenance来源。

### 19.1 Compiler与provenance（最终验收）

- exact Go/C identity、ABI、contract digest稳定；
- 同一physical `(symbol, ABI)`的多个exact declaration保留不同DeclarationRef，ABI不同的无关
  declaration不会污染或否决另一个declaration；
- FuncPC direct copy、same-fact Phi、冻结readonly global保持shadow；
- arithmetic、unknown store/load、interface、heap、C memory和mixed Phi丢shadow并拒绝；
- 不存在地址反查表、symbol-name fallback或pclntab依赖；
- Auto绝不读取TrustedInline contract；
- TrustedInline缺proof、lease、target capability或cost bound时拒绝；
- dynamic candidate refinement相同/可join正例及不兼容负例；
- cross-package summary缺失、版本错配和ABI错配失败；
- contextual helper只接受exact static function argument与合法extraInfo。

### 19.2 Wrapper语义（待建立stdlib E2E）

- arbitrary `syscall.Read`走Auto；
- pollable、nonblocking且lease有效的read走inline；
- regular file、SetBlocking、close/reuse、fd generation变化回到Auto或失败；
- EINTR保持Go retry语义并有preemption边界；
- EAGAIN只在pollable路径注册readiness；
- short read/write、EOF、errno、NULL/word/int32 failure convention不变；
- wrapper内pollWait、deadline、close、semaphore、defer和panic继续传播effect；
- 函数值/interface调用不继承某个静态call-site proof。

### 19.3 Operation/select/cancel（runtime核心已有部分证据，外部IO接线待完成）

- early completion、completion-before-park、duplicate与stale generation；
- ReadyThenTryCommit失败重试；
- irreversible result winner Take与loser Discard；
- select多个ready、default、timeout、task abort和shutdown竞态；
- cancel-before-submit、queued cancel、active abort、logical-only late completion；
- detach barrier、producer admission seal、callback/worker/IRQ strong quiescence；
- queue/source capacity exhaustion返回声明结果，不丢wake、不无限busy loop；
- cancellation不提前释放buffer、descriptor、frame或result lease。

### 19.4 平台（最终验收，非当前完成度）

| 平台 | 最终最小运行验证 |
| --- | --- |
| Native Linux/Darwin | time.Sleep；regular-file回环；loopback TCP read/write/deadline/close；worker饱和/容量；poll stale/cancel；启动期1/8 P与P-neutral parked-result demand injection/no-bounce；运行期P resize与批量/local-deque steal |
| JS/WASM | real later-turn Schedule；timer；Promise完成/abort/late callback；无递归reentry；wasm32 descriptor/layout |
| WASI | clock + pollable fd；`poll_oneoff`/preview equivalent；一个nonpoll import的async或Unsupported路径 |
| RTOS/embedded | QEMU或硬件notification、one-shot alarm、ISR publish、DMA cancel、容量填满、task affinity |
| Baremetal | QEMU main loop、hardware compare、IRQ ring、WFI/WFE、stale generation、IRQ fence、无pthread/libuv依赖 |

最终支持范围要求LLVM 19、20、21、22分别验证CoroSplit、descriptor/frame layout、
module verification和关键E2E；不覆盖LLVM 19以下。这是待完成的验收门，不是当前
通过声明。GC profile亦需分别验证nogc、conservative和最终的exact frame root/pin
contract，compile-only不能替代production platform E2E。

## 20. 当前实现状态与差距

截至2026-07-26，可准确归类为以下三层。

**已进入生产路径**：

- exact declaration/wrapper parser、四维target-neutral behavior model及共享canonical digest；
- managed-required exact C declaration的total、content-addressed identity inventory，以及
  content-addressed generic contract；两类不可变certificate都绑定function/link identity、
  callable/typed ABI和physical C symbol/ABI，contract另绑定scope与behavior；
- EmissionUniverse freeze、`internal/build`分类、SSAFunctionPolicy/SSAPlan与
  当前 `PlanDigestSchema v26` 的端到端传递；
- declaration的progress/affinity/reentry/memory保守投影、wrapper全body分析，以及与
  `noblock/sync/schedulerwait/worker/workeraddr`和assembly certificate的冲突拒绝；
- `CallableContractFacts`/CallableFact/InvocationFact的pointer-free模型、canonical verifier/digest。
  `SSAPlan.CallableContractFacts`以total identity inventory投影显式generic behavior与legacy/未标注
  declaration的content-addressed unknown behavior，并覆盖target-owned refinement、exact site、
  closed join和open unknown Auto；这一catalog仍未成为生产archive和所有consumer的唯一输入。
- target-ownedDefault/TrustedInline双contract、executor-safe小wrapper的exact static call producer、
  SSA `CallTrustedInline`/CallPlan/current digest和physical direct-call resolver的生产闭环；它只授权
  exact edge，允许保守Default的`ThreadAffine|OpaqueExec`在该edge被selected projection替换，
  同时保留`IRQUnsafe`/`MayUnwind`等非契约约束；wrapper body仍被分析，错target/contract/ABI、
  projection lane不一致和缺refinement均失败关闭。
- FuncPCABI0 producer-forward shadow作为worker syscall的硬授权gate，以及target/ABI/arity、
  private carrier和conditional incoming一致性校验；没有任何address-to-callable反查。

**已有基础设施或独立原型**：

- 现有scheduler/operation核心的OperationID、generation、ParkState、WaitSetRecord、
  select/result lease/cancel/detach/quiescence、Timer/Poll/Worker等能力，以及native启动期1–8 P fleet
  target、共享worker lifecycle、P-neutral typed materialization、fleet demand injection/work
  sharing和TCP E2E。这些证明callable模型有可复用底座，不证明运行期P resize、批量/local-deque steal、
  通用backend recipe或各平台已完成。
- target-neutral `NonblockingLeaseGate`的exclusive attempt、generation change、quiescence、
  close/reuse tombstone和duplicate-release防护；尚未与`internal/poll.FD`状态及compiler
  operand/context proof连接。

**尚未完成的关键闭环**：

- 第一版`runtime.LockOSThread`、locked G exact P/M归属和同M foreign call已经闭环；仍缺
  locked G未unlock退出时的物理M退休、动态owner容量和blocking compensation。共享worker继续
  只授权`AnyThread`，thread-local session必须持有当前G的exact lock lease；
- 将当前小wrapper exact-edge proof扩展为绑定operand/resource generation、target capability和
  cost bound的region/context proof，同时保持target-owned refinement约束；
- 与 `SetBlocking`、close/reuse、resource generation/epoch同步的真实NonblockingLease；
- 将剩余逐trampoline legacy `workeraddr`标注迁移为generic callable contract；legacy仅是
  producer兼容输入，不再对应独立consumer provenance；
- 把address-only、尚未进入managed-required集合但已由producer-forward shadow证明的target纳入
  统一identity/facts/archive边界；当前不扩大required inventory，也不做地址反查；
- foreign Callable descriptor、开放dynamic ABI、cross-archive summary与runtime registration/catalog；
- target-owned OperationRecipe/adapter选择，包括worker/readiness/Promise/WASI/HAL/IRQ，以及
  reentry、retained memory/pin的物理处理；
- 将当前`internal/poll` socket attempt/Poll V2/opaque descriptor vertical slice推广到完整FD族；
  扩展真实file/socket/deadline/close竞态、动态P/高并发backend，以及JS/WASM、WASI、
  RTOS/embedded、baremetal production adapter。

因此当前结论是“managed-required C declaration的total callable identity/facts、generic contract、
target-owned TrustedInline exact-edge闭环与producer-forward shadow授权gate已落地”，不是“文件、
网络、标准库或所有平台已完成”。后续应闭合address-only inventory、NonblockingLease/复杂context
proof、descriptor、backend recipe和archive唯一事实流，不再引入地址反查或按API名称分裂的平行机制。

## 21. Review checklist

每个新增或修改的callable/wrapper至少回答：

1. exact CallableFact来自Go推导还是显式C/host contract？冻结ID是否绑定完整behavior
   digest、function/link identity和ABI？
2. Default是否保持正确的MayBlock/affinity/reentry/retention边界？
3. TrustedInline是否是已有refinement，而不是调用点临时放宽？
4. proof是否绑定exact call edge、operand、target和body digest？
5. 是否需要NonblockingLease；lease能否被SetBlocking/close/raw mutation破坏？
6. FuncPC/descriptor事实是否全程前向传播；是否出现任何地址反查？
7. event是readiness、irreversible completion还是reservable？
8. backend是worker、reactor、host还是IRQ；它是否错误地替代了commit语义？
9. cancel-before-start、active cancel、late completion和quiescence分别如何完成？
10. argument/result如何root、pin、copy、Take或Discard？
11. queue/source满时是AwaitCapacity、确定错误还是Unsupported？
12. inline调用的blocking和cost是否都已证明？
13. dynamic candidate如何join；开放flow是否回到Auto？
14. native、WASM/WASI、RTOS/embedded和baremetal分别选择什么recipe？
15. 新增该wrapper是否保持compiler core和scheduler状态机零特例？

任何问题没有明确答案时，默认结果是fail closed。当前exact小wrapper proof不能替代真实
NonblockingLease，不得仅因target拥有TrustedInline就为普通stdlib read/write调用点签发
certificate。forward shadow现已直接拥有最终owner/incoming inventory；后续不得重新引入
consumer reverse或地址反查，legacy `workeraddr`只能作为producer contract兼容输入。
