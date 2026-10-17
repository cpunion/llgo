# LLGo 统一异步执行核心与扩展契约

状态：设计冻结前的实现审查稿

更新：2026-08-30

关联总体设计：[`llvm-coro-runtime-design.md`](./llvm-coro-runtime-design.md)

编译器语义标准化 IR 与统一 lowering 审查：[`coro-ir-design.md`](./coro-ir-design.md)

Callable、调用点、`FuncPCABI0` 前向事实与 `Auto/TrustedInline` 策略：
[`coro-callable-contract.md`](./coro-callable-contract.md)

## 1. 结论

LLVM coroutine 只负责保存、恢复和销毁无栈 continuation。它不是异步模型，也不应知道 timer、文件、网络或某个 syscall 的语义。

LLGo 的异步能力必须先形成一套与 continuation backend 解耦的公共核心：

1. 编译器以 effect 固定点决定函数是否可能挂起，以独立的 value-flow 决定函数值是否需要动态描述符，以 demand 决定入口是否需要物化。
2. Scheduler 只管理逻辑 `G`、可运行队列、park/wake、取消、抢占和生命周期。
3. Executor 负责把可运行 `G` 映射到执行资源，并执行统一的 drain、idle admission、sleep、wake 和 shutdown 协议。
4. Event source 管理 timer、I/O、foreign worker、host Promise、RTOS notification 或 IRQ 等外部事实。
5. Platform adapter 只提供 monotonic clock、doorbell、阻塞/返回 host、线程或中断接入等目标能力。

完成这套核心后，`time.Sleep`、文件、网络和绝大多数标准库同步风格 API 应只增加 source/adapter、固定布局的 typed park recipe 或薄 runtime wrapper。新增普通异步能力不得再次修改 effect 传播、静态 coroutine call lowering或发明新的调度模型；typed recipe只能组合统一的 ParkSet、source、result lease和取消契约。

多 P 的 command shutdown 必须先向所有已启动 owner 发布同一个 sticky stop，
再让各 P 并行执行既有取消清理；任何一个 P 的 worker 都可能依赖另一个 P 的
shutdown defer。不得先排空或 join 单个 P，再通知其他 P。通知不等于关闭 source：
worker、事件注册和 result custody 仍按各自协议 strong-join，最后才回收 owner、
source 和物理 worker。正常 FinishMain 与 pending-source runner reentry 都必须满足
这个顺序。复用现有 atomic stop ingress，不增加每 G 状态或第二套取消协议。
peer stop 和 runnable placement 读取该 word 的 Sealed phase，即使旧 M 的
succession lease 尚未退出也必须开始取消；Quiesced 只用于最终选择稳定 join target
和回收，不能用它延迟 stop 生效、消耗一次唤醒后又重新入睡。

关闭清理仍可调用 GOMAXPROCS；允许改变逻辑 quota，但 sealed stop 之后不得启动新 M。
固定 P1 worker → P2 cleanup 的真实 linked fixture 必须在旧顺序下超时、在修复后通过，
并覆盖清理中提高 quota。host adapter 的 pending-completion gate 还必须证明本地
取消 continuation 不会在 peer stop publication 之前运行。

## 2. 不可协商的设计规则

### 2.1 一个 source function 只有一个 primary body

- `NoSuspend` 函数生成唯一 plain primary。
- `MaySuspend` 或需要可挂起抢占的函数生成唯一 coroutine primary。
- 静态 managed caller 根据 effect 自动选择 direct plain call 或 structured await。
- `BothDemand` 只表示存在两类 consumer，不允许复制完整函数体。
- hard-sync C/host consumer 使用薄 root/reentry adapter；目标不允许同步等待时使用 Promise、JSPI 或明确诊断。

### 2.2 动态表示与异步染色相互独立

函数是否异步由 effect 决定；是否需要 descriptor 由函数值流决定。

只有函数值进入开放或 ABI-visible 边界时才 canonicalize 为 descriptor，例如：

- `any`、interface、reflect；
- global、heap、map、channel 或未知 aggregate storage；
- 未知包或 archive boundary；
- C/host callback registry；
- 无法闭合目标集合的动态调用。

FuncRep v2 descriptor可以发布可选plain entry，并在outcome与coroutine之间发布唯一structured entry；consumer也可以围绕archive已发布的物理primary生成typed薄adapter。它不得包含两份source body，outcome与coroutine也不得同时占用structured entry。

### 2.3 新 API 不创建新 compiler semantic family

Compiler 只保留少量语义族：

- `Yield`：主动或抢占安全点切换；
- `Park`：等待 scheduler/runtime operation；
- `ForeignOp`：可能阻塞的 syscall/C operation；
- `HostOp`：需要返回 host event loop 的 operation；
- `Spawn`、panic/defer/Goexit 等 Go 语言控制语义。

Timer、channel、netpoll 和普通异步 wrapper 在runtime侧都复用统一的
`ParkSet/source/operation lease/cancel`模型。Compiler可以保留少量、固定布局的
typed park recipe，例如`time.Sleep`使用一个专用intrinsic来分配opaque frame
storage并冻结park/resume状态分派；这不是独立调度模型。不得为每个fd API、
syscall number或普通标准库wrapper继续增加opcode，它们应落到通用Park、
ForeignOp或HostOp recipe。

## 3. Compiler contract

### 3.1 三个独立问题

| 分析 | 回答的问题 | 不允许承担的职责 |
| --- | --- | --- |
| Effect/Exec | 函数是否可能挂起、等待哪类外部能力、是否需要抢占或线程亲和 | 不决定函数值物理表示 |
| FuncRep/value-flow | 函数值能否保持 direct，还是必须使用动态 descriptor | 不决定是否复制 body |
| Demand/emission | 当前链接单元需要哪些 primary、descriptor 或 boundary adapter | 不重新推导 effect |

Effect 通过普通 direct/defer/dynamic managed call graph求最小固定点。调用一个 suspendable callee 会给 caller 加入 structured-await effect；`go` target成为独立 scheduler root，不把被启动任务的等待 effect传播给启动者。

### 3.2 跨包规则

独立 package archive 必须携带 producer summary，至少包含：

- stable FunctionID和 ABI version；
- exported/address-taken function 的 Effect、Exec 和 primary kind；
- 参数、结果和 aggregate 中 function leaf 的 canonical FuncRep schema；
- primitive/suspend contract依赖；
- physical ABI/layout hash；
- bounded-preemption cost或unknown标记。

Import 时 summary 参与与源码函数相同的固定点；link 时验证版本和 ABI hash。Whole-program cache digest不能替代 producer archive summary。

私有 helper 没有 consumer SSA 声明时，仍以 producer summary 的精确 FunctionID
加入同一个 Graph，不能补造 `ssa.Function` 或把已知调用降级成 unknown dispatch。
`SSAPlan.Functions()` 只投影真实源码；`ImportedFunctions()` 投影这些 archive leaf。
精确 lowered edge 统一传播 effect/demand，producer 已闭包的 service/predicate fact
在该 edge 上进入现有固定点。Plan digest v43 同时绑定 imported fact（含符号、完整
ABI hash、true/false predicate mask）及最终 FunctionPlan，未使用的 helper 不成为 root。
调用方首次把 imported direct 函数变成函数值时，可单向提升为 Dispatch，包装同一个
producer primary；不改参数/结果 schema、effect、entry、symbol 或 ABI hash，不要求库
预先取址，也不生成第二份 body。plain/outcome/coroutine capability 仍须精确成立，
raw/opaque、不匹配的 primary 和反向降级均不授权。未 demand 的值不因此产生符号。
静态 bodyless producer 的 helper 签名由完整返回 tuple 推导，复用 producer/consumer
同一个 ABI hash 构造器，并绑定实际 target layout。物理调用复用现有 descriptor/thunk、
child await 与 cleanup drainer；不增加 runtime 字段或第二套调度/取消协议。当前测试
覆盖 native/wasm32 CoroSplit 前后，以及真实 native archive 的运行期显式取消。
context-free source/archive producer 可在物理 guard、生命周期证明和精确 helper
绑定都成立后发布冷的 `HasDelivery` tail。descriptor 与普通两字函数值在编译期同时
保留类型信息；不能从擦除后的 code word 反向找回元数据。普通 descriptor 仍是原有
九字段 base；resource tail 只存版本和两个已有 managed helper descriptor，不扩大
funcval。捕获环境、receiver/bound-method adapter、raw/plain capability 和 FFI 第二次
资源交付仍是独立 gate，不能仅凭类型元数据或符号存在而放开。普通 main 退出不等价于可等待的显式任务取消，
正常丢弃返回值（包括 defer）也不自动 drop。

### 3.3 Lowering matrix

| Caller | Callee/值 | Lowering |
| --- | --- | --- |
| plain | plain direct | 普通 direct call |
| coroutine | plain direct | 当前 resume episode 内普通 direct call |
| coroutine | coroutine direct | 创建/取得 child continuation并 structured await |
| coroutine | descriptor | 检查 capability；plain slot直接调用，outcome slot同步提交显式结果，coro slot创建child并await |
| hard-sync boundary | coroutine | typed root/reentry adapter；不复制 body |
| plain managed body | 可能 coroutine 的开放动态值 | caller必须先被固定点染色，或在不允许的 hard boundary诊断 |

### 3.4 跨 suspend 数据生命周期

必须区分三类数据：

1. 普通 SSA value或local只在同一 coroutine 内跨 suspend 存活：由 LLVM CoroSplit 放入 frame。
2. runtime 在 park 期间需要访问的 wait state：放在稳定 `G` 或 scheduler-owned operation record，不保留调用者临时地址。
3. syscall/host/worker 在异步执行期间需要访问的 Go object：放入 compiler/runtime共同定义的 argument/result record，显式保根并在需要时 pin，直到 terminal acknowledgement。

Compiler 不得按 `time.Sleep`、`read` 等函数名证明 prepare/park/retire。若确需 frame-borrow，必须使用通用、版本化的 `SuspendRegionContract` 描述角色、retained roots、alias closure、lifetime end、GC policy 和 no-preempt region；优先通过稳定 operation record消除 borrow。

### 3.5 Resume gate 与逐 frame cleanup

每次非final suspension epoch恢复时都先进入compiler-owned resume gate。initial entry、普通yield/park、child await和conditional suspend的true-resume边都执行gate；conditional false边直接进入normal join；final suspend、CoroSplit ramp和destroy helper永远不执行gate。直线型gate只能读取并消费`RunDecision`；需要转入cleanup或select reconciliation时，compiler必须使用可终止的dispatch gate，并保留一个compiler-owned normal block作为SSA/PHI的唯一普通后继。全局default与单个suspend site override互斥，不能让任意callback夺走CoroBuilder的logical tail所有权。

Gate不能只看task cancellation后立即销毁frame。每个suspend site先完成本地reconciliation：child await读取parent-owned completion；park/select按exact ticket取得outcome、case和result lease；被task stop压制的selected result仍按`OperationID`显式discard。然后才进入共享cleanup入口。这样completion、operation cancel、panic/Goexit与task stop不会因同时可见而漏掉payload或physical resource。

每个可能cleanup的coroutine frame使用可跨suspend的显式状态机，而不在LLVM coroutine之间保存native jump buffer：

```text
Idle -> Draining(cursor, control stack)
     -> AwaitingDeferredCall -> Draining
     -> PublishingCompletion -> FinalSuspended
```

defer参数在statement执行时求值一次；cursor保证LIFO defer即使自身park也只执行一次。`Return/Panic/Goexit/Abort/Shutdown`是不同control kind；defer中的新panic压在原control之上，recover只消费direct deferred invocation携带的`RecoverToken{panicGeneration, ownerFrame}`，原Goexit或task stop在该panic被recover后继续。`Abort/Shutdown`运行defer但不可被recover；`os.Exit`仍不运行defer。

Child的frame可能在parent恢复前销毁，因此非普通终态不能只留在child header或G的一次性cancel token。structured await使用parent-owned `CompletionRecord`保存`Return/Panic/Goexit/Abort/Shutdown`及panic identity；普通用户result另存于parent-owned typed result slot，不占用Return的nil payload。CompletionRecord由单一scheduler owner发布/消费，不需要跨线程原子操作；外部producer仍只通过operation记录和既有发布协议交接。parent先读取kind，只有`Return`才读取普通result。root终态同样在destroy前复制到稳定boundary/G storage。现有cleanup-free terminal panic和直接command destroy只能保留为证明无defer/recover的受限快路径，不能作为通用Go unwind。

普通 `runtime.Callers` 不得为排除 deferred-panic scope 而遍历全部 await
祖先。每个真实 child 连接点（普通/兼容 await、bounded eager await 和 foreign
reentry）统一通过 `linkAwaitChild` 发布一个继承的 frame 位：recover-armed
completion 开启作用域，普通 managed child 继承，legacy empty completion
终止继承。该位与 borrowed-storage 共用原有私有标志字节，不增加 Frame、Header
或 G 大小，不引入计数器、TLS 或单独的恢复栈。零位只允许 O(1) 否定观察；正位
不能授予恢复权限，仍须验证精确 child identity、CompletionRecord 状态和 panic
payload。recover-taken 仍保留作用域直到 deferred child 返回；恢复 parent 时自然
使用其原有继承位，嵌套恢复不需要额外 push/pop。终态、销毁与资源清理协议不变。

该 owner-only 元数据观察本身不能为了扫描祖先而成为一个新的可抢占 observer
frame。仅底层 `deferredPanicTraceScope` 使用现有 `llgo:nopreempt` 证书；它不分配、
不访问 I/O，只调用叶级元数据谓词。普通 Go 调用者仍自动染色，不逐层标注。
证书只压制本函数的 loop/budget seed，不压制 callee 的真实 suspend/unwind，也
不免除其他物理校验。同步 outcome 的循环体同样必须消费冻结的 exact-body 证书；
缺失、空证书或另一函数的证书均不能授权。普通未标注循环继续使用抢占 poll，不能
从 `NeedsPreempt` 恰好为零倒推出该授权。

当前工作树已完成可独立闭环的task-stop和动态cleanup子集：`CompletionRecord`实际发布并逐层消费`Return/Panic/Abort/Shutdown`，另用`ReturnRecovered`提交direct deferred invocation的recover；frame-local terminal status使普通defer返回不会把`Abort/Shutdown`覆盖成`Return`，defer中的panic作为overlay优先，只有精确的`ReturnRecovered`会清除原panic。zero-ticket gate以及Channel/Worker/Timer/Poll/child-await恢复路径都会保留精确cancel kind；取消命中await后仍先消费child completion，并按五种状态重新进入同一个drainer。函数一旦存在循环注册，全部defer site统一进入frame-rooted异构LIFO，typed record在调用前pop/copy/free；无环site（包括可闭合的descriptor defer）仍保留一次注册的typed slot fast path。当前证据覆盖compiler可枚举的owner-local defer与direct recover；range-over-func的跨frame`DeferStack`、`Goexit`、root boundary outcome及完整GOROOT panic/defer矩阵仍未完成。

### 3.6 资源型结果：物理完成不等于调用者接管

当前采用 **typed borrowed output + 预注册普通 Go defer**，复用已有借用、
physical join 和 cleanup drainer，不增加另一套资源 capsule 框架。2026-10-16
已完成真实 native adapter 验收；公开 Go 返回交付和各平台标准库接线仍未完成。
普通 scalar 的 Take/Discard 已闭环，但清除一个 fd、host handle 或 opaque C
pointer 的数值，不等于释放它代表的资源。当前 worker/host 的成功文件路径可运行，
仍不能据此宣称资源返回与任意 task cancellation 的组合已经安全。

结果 sink 的地址不能比生产调用活得更久。ProgramIR 在所有 call-site contract
冻结后，复用已有 allocation/parameter borrow analyzer，分别证明 borrow 返回值、
initializer 第一个参数和 drop 第一个参数的完整地址图；不增加另一套逃逸分析。
普通 managed Go helper 递归推导；C/worker 调用只消费已冻结的
`ArgumentsBorrowedUntilCompletion`，不以 inert stub 证明生命周期。全局保存、
interior address 逃逸、interface/unsafe 转存、闭包捕获、go、未知动态调用等无法
证明的边均拒绝。当前证明保守地拒绝保留借用地址的 defer，不授予 detached work
权限。物理 preflight 要求三个精确 proof identity；旧产物不能用类型相同绕过检查，
guard ABI v2 进入现有 source/library ABI hash。该证明只有编译期开销，不增添 frame
字段、分配、检查指令或源码注释。

最初的确定性回归将真实打开的 fd 经过现有 WorkerOperationSource、HostOperationAdapter 和
ResumePacket。正常消费正例通过；以下两种取消都会留下未关闭的 fd：

- producer 已 Post，owner 尚未物化 packet；
- packet 已物化、旧 source/adapter 已回收，G 迁移后才取消。

`HostOperationAdapter.Retire` 在 owner materialization 阶段发生，不是最终接管点。
`TakeResumePacket` 可因晚到的 task stop 抑制已复制的 scalar 并清空 packet。
不能仅给 Retire 加一个 disposition 参数、延迟一次 host microtask，或禁止迁移
来修补：这些都没有覆盖全部所有权窗口。原始负例及 optional capsule 的核心
实验补丁、race 记录已归档；未接入平台的 capsule、binding kind 和 source capture
接点已移除，不留没有消费者的并行机制。现有生产方案的真实 gate 见下节。

守卫只针对 `open/openat/accept/dup` 等已知 acquisition adapter；未知 C 返回
不能凭整数或指针类型猜测为资源。不对任意 Go int/unsafe.Pointer 的复制、存储、
interface 化推导线性所有权；普通 Go 调用继续自动染色，不增加传播性注释。
logical outcome、取消、winner 和 source retirement 均仍由既有协议唯一决定。

#### Go wrapper 的跨 await 临时所有权

`syscall.Open`/`os.Open` 并不总是在第一次底层 open 返回后立即把 fd 交给用户。
例如 JS wrapper 随后还会 fstat、处理目录信息或注册自己的 descriptor table；
native wrapper 也可能继续 fcntl/初始化。取消可落在这些 await 之间。

这里优先使用普通 Go 的临时守卫和 defer，而不是实现一般 resource-effect/SSA
所有权分析。已知 acquisition wrapper 在调用底层 operation **之前**建立空守卫、
注册 defer；底层借用该守卫的 typed POD 输出槽写入成功资源。之后 fstat/fcntl
等普通 await 不改变守卫，defer 自动染色并走现有 cleanup drainer。成功时明确
transfer，失败/panic/cancel 时释放。不能等一个独立 async helper 已返回 fd 后才
首次注册 defer：helper 返回前的取消窗口仍无人负责。

同样，成功 return 前简单设置 `owned = false` 并不足够：child frame 销毁后，
parent 可能尚未读取结果就被取消。现有 `awaitCoroChildWithRecoveryAndConsume`
已经提供可复用的骨架：ordinary 与 cancellation 两条 lane 都先消费 parent-owned
CompletionRecord，随后才进入正常结果读取或共享 cleanup。但当前骨架**没有资源
drop/claim 信息**，不能称为已经完成的 resource-atomic handoff。

公开交付方案已确定为 **existing typed result slot + temporary cleanup**，正在分阶段
实现，尚未启用 public API seed。不得把设计或 private primitive gate 记作公开调用已验收：

结果 tuple 统一由 `coro.ResultSlotType` 构造，字段为 `R0`、`R1` 等。底层 Go helper
用匿名结构体别名声明相同类型，直接接受这个 typed slot；不以字段布局相似为由放宽
具名类型身份，也不为私有字段的包身份另建 representation-cast 或 synthetic-SSA
机制。coroutine、outcome、dynamic descriptor 三条路径共同使用这一构造器。

1. parent 在 child 能开始 acquisition **之前**初始化并保护原有 typed result slot；
   child 把资源写入这个槽，之后才允许销毁。普通 `CompletionReturn` 仍保持 nil payload，
   不把资源塞进 CompletionRecord，也不新增 G/ResumePacket 字段。不得让资源只存在于
   即将销毁的 child guard。
2. parent 的 cancel lane 消费 completion 后 drop；normal lane 在 Return status
   已验证后，一次性 claim 到预注册的标准库守卫，或提交给普通 Go caller。这里的
   guard 只表达资源归属，决议仍只有现有 RunDecision，不另做 winner arbitration。
3. public `syscall.Open`/`os.Open` 正常结果交付给 Go caller 后，原有 Go 语义恢复：
   用户负责 Close/自己的 defer；不继续追踪 fd 的任意 int 复制，不替用户猜测释放。
   标准库内部多 await wrapper 使用前述显式普通 Go 守卫，不靠无限向上推导 contract。

物理 emission 的空间约束进一步明确如下，仍属于待实现的 public-delivery gate，
不能用设计替代运行证据：

- 一个 physical frame 同时至多等待一个直接 child，因此所有临时调用守卫共享一个
  active tag，而不是每个 call site 永久增加一个 bool 或分配一个 runtime guard 对象。
  各 site 继续使用已有 typed result slot；drop 所需的 env/参数按其规范签名捕获，
  不从代码地址反查、不额外复制整个结果 tuple。生命周期结束后清除 pointer roots。
- producer 的 nil-output fallback 可以与它自己的 acquisition child 同时存活，不能
  与上述 active tag 混为一个标记。它仅在没有 parent-owned 输出槽时启用，初始化后
  才承担资源；child physical destroy、panic、取消和 discarded `go` entry 均仍受其
  保护。正常有 parent slot 的入口不得重复初始化或抢先 drop parent 资源。
  **普通正常 Return 丢弃结果不等于 Close**：例如 `go syscall.Open(...)` 正常返回时，
  不能仅因返回的裸 fd 无人保存就替 Go 程序关闭它。fallback 仅在 panic/取消等未交付
  路径 drop；source RunDefers 尚未到 terminal Return 时保留归属，实际正常 Return
  commit 后才撤销而不释放。这复用既有 terminal status 与 cleanup continuation，
  不新增一个动态“是否已提交”字段。正常丢弃必须有 fd 仍存活的兼容性对照。
- 临时 child guard 的 drop 在共享 cleanup drainer 中先于普通 source defer 执行；
  fallback 的最后归属则在 source cleanup 之后结算。两者均在调用 drop 前撤销本次
  active 标记；drop 的 await/panic 重新进入原 drainer 时不能重复执行同一资源记录。
  drop helper 负责先清空自己的 ownership sentinel，再执行可能挂起的物理 close。
- initializer/drop 的图边、exact target、typed capture 和 source/cleanup placement
  都在 ProgramIR freeze 时补齐，再参与原 effect/demand fixed point。static、dynamic
  descriptor、outcome、spawn/defer、reflect 不得各自创建另一套 cleanup 或 winner。
  没有资源契约的调用保持零额外 tag、存储和 hook。

dynamic publication 的冷元信息采用 `HasDelivery` capability 和可选 tail：
`{base-v2, {version, reserved, initializer-descriptor, drop-descriptor}}`。
普通函数仍只有原九字段 base，funcval 仍为 `{descriptor, env}`。tail 从完整、
含 target padding 的 base 之后开始，不能在 wasm32 上占用 base 的尾部 padding。
helper 使用现有 context-free managed descriptor，精确校验 output pointer、原始参数、
ABI hash 和 void result，不另设 raw callback ABI、地址反查表或调度器。
仅有已绑定 result/cleanup transaction 的 consumer 才可打开 tail；读取必须被
`HasDelivery` 正分支支配，普通 unguarded dispatch 拒绝该 capability。已有窄化
structured-only fast path 要求无 delivery 的冻结证明，不能拿该证明绕过资源 guard。
最初的 publication gate 要求调用图、frame guard 和 library helper 同时绑定。
当前 native 实现已具备该绑定，并已接入公共 POSIX Open/File seed；reflect 的
第二次交付使用下述同一资源 guard，目标能力和最终验收范围另记于 acceptance ledger。

Native `reflect.Value.Call/CallSlice` 本身借出精确的 `struct{R0 []Value}`，在任何
源调用前把一个独立、逃逸的托管调用记录绑定到其 private State。绑定是现有
CallSitePlan 的 inline projection：只能对本 producer 的原始 borrow SSA 值执行
一次，不能绑定 nil、外部地址或结果槽本身；load 只授权给对应的私有 drop helper。
带此绑定的 drop 不得读取任何原始参数，其普通 Go 调用和函数值 publication 均拒绝。
完整 state 绑定沿 exact-return forwarder 传递，外层 wrapper 不得再次覆盖它。
标准库私有 `Value.call(op,in) []Value` 也保留原签名和独立 guard，避免补丁选择前
生成的 promoted wrapper 失去 ABI 匹配；所有入口共享同一个 `callInto` 实现。

反射仅在 descriptor 的 `HasDelivery` 分支打开 tail-v3，分别校验 helper 的
独立 ABI hash、NoCapture、void result 和 capability。原始 `ffi.CallLLGo` 仍拒绝
无 guard 的资源调用。受保护 invocation 先保留物理参数的值快照、初始化带 State
prefix 的完整 native 输出，再执行原来的 managed libffi 调用。native 返回后，
仅通过无调用的指针 load/store 把 prefix.State 转入反射调用记录并清空 prefix；
装箱和外层 caller 接受之前，原来的 compiler drainer 仍负责 drop。drop 调用前
撤销其记录，只为该调用恢复 native State，并经原有 cleanup shield/join 结束后清空。
正常 caller 接受则清空外层 State；返回的 Value 可保留结果数据，不继续保留调用快照。

已知绑定方法使用 compiler 发布的 exact Ifn descriptor 和真实 receiver context，
普通 `MakeFunc` 继续使用原有 callback descriptor。函数类型相同或结果是 fd 都不
产生资源能力。这里不新增 G/P 字段、source、scheduler 路径、地址反查或用户取消屏蔽。
无 libffi/host capability 的 target 仍在现有动态调用边界明确拒绝，不能以 native
反射通过推断 WASM/baremetal 反射已经完成。

隐藏的 typed initializer/drop 调用同样进入已有 `SSALoweredCall` 清单和 effect 图，
不伪造 SSA 函数，也不为发布冷元信息强迫静态 helper 改为 Dispatch。动态调用的
临时 custody 条件在 effect fixed point 之前消费已有 scalar/CHA/开放目标证明；
闭合且无资源的目标族（包括 nil-only）删除隐藏 helper，并保持原 FunctionPlan。
未知 managed 目标只保守增加已有 managed-call effect 和 owner-local cleanup，
不向普通 Go 调用链传播“资源所有权”。producer 身份来自冻结的本地 body fact
或校验后的 archive delivery record；exact source occurrence、producer fact 和
隐藏 helper 的条件关系全部进入 plan digest。物理 emitter 不重新推导这些事实。

前端的 exact/conditional helper 共用 ProgramIR builder 的一个记录与合并入口。
pointer-free `ManagedEdge` 以显式 `managed_dispatch` 区分 typed descriptor 调用
和 exact FunctionID；后者缺少 target 仍是错误，前者禁止混用 exact target、
no-unwind 或 terminal-elision shortcut。它不引入 synthetic SSA 函数或第二张调用图。
冷 helper 元信息复用普通 descriptor/typed-thunk 的同一 primary 构造器，不能
为此将静态 initializer/drop 染成 Dispatch 或复制函数体。共享构造器仍须保留
source facade identity 与 canonical physical primary 的区别，不能丢失 go:linkname
函数值的 source signature。

program capability/predicate 闭包同样按这个调用域消费记录：exact helper 必须有
冻结目标；managed-dispatch helper 不得为了满足该要求而伪造目标。动态 delivery
helper 绑定的 source CallPlan 必须仍为 active，其已知 producer 的 initializer/drop
依赖沿原有 producer 调用闭包传播，open Dispatch entry 仍是潜在 program root。
未知 helper 不能仅因未知就启用全部可选服务；已闭合的无资源族不能获得这些依赖。
这是对同一个最终 CallPlan 的投影，不是新的 capability 调用图或第二套染色。

动态 consumer 现已从真实源码 occurrence 自动冻结 typed recipe 与 helper placement，
无需手工登记。最终 `CallPlan.ResultDelivery` 同时决定 pointer-free ledger 和 source、
cleanup、result-drop 三种 placement 的 helper 是否保留；仅从清单中找不到 helper
不能作为省略依据。普通 Go body 的 `nounwind` 证书也不能借给动态选择的 helper。
条件与调用域正交：initializer/drop 可以是 managed descriptor invocation，而
result allocator 是准确的静态 target；两者消费同一个 occurrence 条件并进入原调用图。
条件不能同时携带 raw/no-unwind/unwind-only/terminal-elision shortcut；exact target
照常验证 canonical inventory、需求和 effect 传播，并把 target 与条件共同绑定到
digest。核心/前端元信息通过后，物理 allocator 仍需单独的 capability/placement gate。
源码 defer 明确丢弃返回值时，非资源 static/descriptor child 使用已有 nil-output ABI，
不分配或加载只为丢弃而存在的结果 aggregate。discard 是编译期 invocation 语义，
与直接 recover 权限正交；内部 helper 不因丢弃 void result 而获得 recover 权限。
有资源 guard 的 defer 仍传入受保护结果槽，completion 消费后才 claim；不能把
nil-output 优化用来绕过资源保管或条件 allocator。
大型动态 defer 的 custody storage 仅在 `HasDelivery` 正分支内分配，借用现有
已冻结 runtime allocator target，并以 source occurrence 独立身份进入同一条件调用图。
该分支外的 base-only descriptor 仍使用 nil output；不能提前把大 aggregate 分配到
ramp 或 coro frame。现有 owner frame 只保留 typed result-address word，normal claim
和 drop 都按原 active tag 清除它。runtime dependency 不在冻结清单时，只有最终 active
consumer 在 physical preflight 明确拒绝；closed nonresource 条件仍正常消除，不从 emitter
补目标或放宽闭包。native/wasm32 的 gate 必须验证：移除 capability 正分支后 allocation
不可达、分配次数与 exact ledger 对应、pre/post-CoroSplit 有效且 frame 不包含大 aggregate。
物理 consumer 复用原结果槽、每 frame 一个 active tag、原 cleanup drainer 与 child
transaction；仅额外保存 drop descriptor、producer env 和准确参数。helper env 固定
为 nil，不保存冗余 word。初始化完成才 arm，completion 消费和 result load 后才
claim，drop 前先清除 tag/captures，内部 helper 没有 source-defer recover 权限。
异构循环 defer 只为其尚未在 drainer 构建前出现的结果保留 typed address，不能依赖
emitter 顺序。closed nonresource 不产生这些 storage 或 helper。static/descriptor
helper 共用唯一 frozen-record validator/observer，架构预算不得提高。

该 consumer 路径已有 native/wasm32、完整 SitePlan、pre/post-CoroSplit 验证，覆盖
普通动态 call、局部 defer 和循环 defer。后续资源实现已接入 producer publication、
bodyless archive helper、external defer owner、allocator/helper 绑定及 loan proof；
具体冻结版本的实际资源与反射验收以 acceptance ledger 为准。无契约 dispatch 和
unguarded FFI 的拒绝门继续保留。

启用 seed 前，必须同时具备：producer borrowed-address/fallback、static normal/cancel
交付、dynamic descriptor 可选契约与 ABI 校验、库元信息消费、defer/go discarded
entry、reflect 第二次交付，以及标准库 wrapper 的预注册 adoption。range-over-func
的外部 defer owner 与当前 yield frame 的临时 guard 必须区分，不能错误地把后者
挂到父函数；缺口应明确拒绝而非借普通函数指针静默绕过。每条路径都要有真实资源
计数及故意移除 guard 的负对照，最后再启用 public API seed。

底层 acquisition 必须在 physical producer 内写入借出的槽，而不是等 generic worker
返回一个裸 fd、回到 Go wrapper 后才复制。当前真实 C adapter 使用 `openInto(out, ...)`
证明这一点；标准库的 libc/host acquisition 也必须有等价 adapter。输出槽不得逃出
physical join 的借用期；trusted adapter 的 noescape/结果写入契约不能由 C 类型猜测。
大型 result 的 fallback 也不能在 parent 已提供槽时额外 eager 分配一份 managed
aggregate。当前 closed-static 实现只在 nil-output 分支分配，普通借用路径不分配
第二份 aggregate；冻结 helper placement 与 native/wasm32 pre/post-CoroSplit gate
同时约束这一点。resource-only cleanup 直接使用 terminal drainer，不合成源码
RunDefers 和额外的返回值 spill/copy。该机制仍须随完整 public-seed 路径一起验收。

只在有限 produce/transfer/release 边界保留准确事实，不改变普通 Go API 类型，
不要求给所有调用者加注释。内部 acquisition
adapter 的 helper 链必须闭合：若在 raw resume 与受保护 wrapper 之间还隔着未声明
的 coroutine return，验证必须拒绝宣称安全，而不是提前 claim。开放 ABI 的资源
行为由边界已有明确契约承担；不新增针对任意 Go int 逃逸的编译错误。

release 进入现有 task-cleanup 屏蔽期后，不因新一轮取消再入或提前终止；新的 panic
或 close error 只改变既有 cleanup outcome，不使同一个资源重复释放。若需要 host
close callback，只有 callback physical join 后才能撤销 custody。此处要复用已有
cleanup cancellation shielding，不能另建任务调度/取消状态机。

2026-10-16 review 明确区分 **已领取 task cancellation 的 cleanup** 与 **正常返回时
开始的内部资源 drop**。Go main 返回不保证其他 goroutine 的普通 defer 执行完毕；
冻结 v74 的 normal-defer 诊断虽以 86 失败、fd 已自行回收，但不能据此要求屏蔽
所有源码 Go defer。广义屏蔽的 v75 实验也保留其 channel-close 失败记录，不作为
正向验收。新的 `coro_claimed_cleanup_channel` 专门验证 LLGo 已有 structured shutdown：
两个任务先领取 stop，再在 cleanup 内通过 channel rendezvous 并显式 close fd。
它是内部生命周期回归，不把 Go 未承诺的 main-exit 行为变成通用语言保证。

隔离开发分支已有 core scope 原型：复用既有一字节 task-cancel phase，增加
Shielded/ShieldedRequested 两态，不扩展 G、Frame、ParkState、CompletionRecord 或
operation。它只延后 task stop；原 operation/context cancellation 与 scheduler
preemption 仍有效。exact outer owner 的 receipt 只在 child strong join 且 completion
已消费后结束；嵌套调用不取得新的 outer receipt。结束时返回并 claim 延后的 stop，
编译器必须保留其 Abort/Shutdown cleanup base 与原 panic overlay，不能返回用户正常
continuation，也不能赋予 drop 直接 recover 权限。它不是用户可用的取消 mask。
编译器 static/dynamic async helper 的 begin/end 已接入同一 await transaction，且仅
用于内部 cleanup invocation；source Go defer 不新增 scope。End 发生在 child joined、
completion 已消费之后；延后的 stop 只修改 cleanup base，保留 panic overlay。同步
outcome/plain helper 没有挂起窗口，不增加异步 scope。native/wasm32 CoroSplit 前后
IR 验证内部 helper 有 scope、源码 Go defer 和普通调用无 scope；架构预算未提高。

compact channel 的旧 fast path 只接受 None/Idle，不能用于已领取或屏蔽中的 cleanup。
现在 prepare、commit 和 local/routed completion 共享有效 cancellation 状态检查，
只让不会中断当前 operation 的状态完成；真正 Requested 仍走原取消处理，非法组合
拒绝且不修改 spill。兼容 prepare 重建 ParkState 时保留 task receipt，不重置其原因。
24 种准备/完成组合、14 个非法或 interrupting 输入、全 core race/shuffle 和 Node/wasm
测试通过。冻结 v76 的真实 linked regression 和 10 次重复运行通过，旧 v75 compiler/
runtime 跑相同 v76 leaf fixture 则 abort；两侧 source-after 均匹配。这个回归验证已
领取取消原因的 channel cleanup，不能代替 public delivery 的端到端验收。此原型不
表示公开资源返回、标准库 adoption 或全部平台已经闭环。

真实 fd 的 `TestResourceDeliveryBoundary` 是这个差异的永久 core oracle：child physical
destroy 后再取消，normal/cancel lane 消费完全相同的 nil-payload Return；parent guard
存在时关闭一次，故意移除 guard 的负对照会泄漏，然后由测试显式清理。它不是 public
Open 的编译器验收，不能用 `Return committed` 或 child 已销毁替代 parent 接管证明。

#### 优先实现：借用输出槽与普通 defer

已知底层 acquire adapter 可以接收指向 caller-owned POD 的输出地址（如
`open_into(*int32)`），与 read 填充借用 buffer 相同：producer 只写资源值，不访问
Go frame 元数据、G、recover 状态或另一个调度器。wrapper 在提交前初始化空 guard
并注册 defer；worker 的 Post/strong join 提供写入到 owner 读取的交接，之后取消
即使丢弃 scalar 返回值，普通 defer 仍持有成功取得的 fd。close 自动染色，继续使用
既有 cleanup shielding 和物理 join；同一个 fd 只尝试释放一次，不盲目重试 close。

`runtime/_testgo/coro_resource_borrowed_fd` 已在真实 LLVM native 编译产物上重复
50 次通过：16 次正常 acquire/close、一次完成前取消、一次后续 await 取消、挂起
frame 期间 GC，以及两个 close worker 同时等待时另一个 G 仍可执行并释放它们。
C 侧最终核对 18 次 open/18 次 close 和全部 barrier，再输出完成标记；此证据不依赖
进程退出隐式关闭 fd，不使用 optional capsule、新 intrinsic、resource effect 或
额外的 G/packet 字段。对应 opt-in gate 为 `LLGO_CORO_RESOURCE_ACCEPTANCE=1`。

这只闭合 physical operation → **同一个预先受保护 wrapper** 的窗口。不能在 helper
返回 fd 之后才建立守卫，也不能据此宣称 `syscall.Open`/`os.Open` 的多层 child→parent
公开结果交付已经完成；该部分仍须按前节独立证明。

Node 的私有 `llgoFSOpenInto` 也已实际验证相同边界：typed `*int32` 初值 -1，
成功 callback 在 Post 前写入，Post 前取消由 host 先异步 close，Post 后取消
只有 Go 预注册的 defer 负责。真实六个 fd、fstat 后取消、GC、双 close 等待期间
另一个 Go cleanup 前进均通过；协议负例覆盖 FD 0、memory.grow、重复 callback
和失败 close 不 ack。executor turn 使用 host task，避免 runnable Go 循环的
microtask 链饿死实际 I/O callback。公开 `syscall.Open` 仍保留原调用路径，WASI
也尚未接入此借用方案；不能把私有 primitive 验收当作两者公开结果交付已经完成。

#### 有限 delivery recipe 与统一消费

只有明确的 acquisition 边界和经证明的纯转发 wrapper 持有可选 `DeliveryRecipe`。
这个事实绑定 exact function identity、source/result ABI、初始化函数和 typed drop
函数。它不是沿 Go 调用图传播的资源 effect；普通调用者只产生当前 call-site 的临时
守卫，不能因为调用了 Open 就被认定为资源生产函数。未知 C 的 int/pointer 返回仍不
猜测所有权。`Dup2(oldfd, oldfd)` 不产生新资源，不能套用 Dup 的 acquire 契约。

源码侧使用私有 `llgo.coroBorrowResult(init, drop)` 声明借用，两个参数必须是编译期
可确定、无捕获的 Go 函数身份，签名由 producer 的规范化参数/结果机械生成。它们不
物化为函数值，不从运行时地址反查；普通函数值发布仍保留原有 descriptor 语义。
精确 recipe 只冻结一次到已有 ProgramIR，普通调用者不继承 acquisition 身份。
fallback initializer/drop 的隐藏调用也纳入已有 lowered-call 图，不依赖源码额外调用
helper 来触发染色；两者独立的 suspend/exec 需求均通过原固定点自动传播。
当前实现只完成源码事实与自动染色的前端验证，物理 guard 未绑定时后端明确拒绝，
不会因为出现这个声明就把尚未保护的 hidden output 指针交给适配器。

源码冻结现已在所有 producer 收集完成后，将 exact static call/go/defer 绑定到同一个
producer fact，并同步既有 owner SitePlan；不复制 helper ABI 或新增资源调用图。
producer 的 discarded-entry fallback，以及实际执行 initializer/drop 的普通调用者和
defer owner，均在原 local body facts 中获得 `NeedsCleanupFrame`，排除无 cleanup 的
outcome-plain 优化。父 Go caller 不执行 spawned child 的 initializer/drop，因此不增加
该父函数的 helper edge 或 cleanup 标志；间接普通 wrapper 和常量死分支也不继承资源
所有权。父调用者的隐藏 helper edges 进入同一个 lowered-call 固定点，不靠注释触发染色。
对应前端与单一架构 authority gate 已通过，未提高任何架构债务预算。此处仍只是物理
接线的先决条件：缺少实际守卫时，preflight 继续以准确的 unbound-guard 错误拒绝，
不将隐式 cleanup 伪装成一个不存在的 source defer，不算公开交付验收完成。

私有 producer 的返回证明采用有限结构：borrow 必须先于源码 call/go/defer，所有
正常 return 按顺序直接读取同一个 borrowed tuple 的各字段；开始读取后只允许其余
字段投影和 return。不能先缓存 fd，再调用可能 await/改槽的 helper，最后发布旧值；
也不接受在表达式求值后执行 RunDefers、再回写缓存结果。普通 Go wrapper 没有这个
限制，也不会自动成为资源 producer。底层 adapter 可在 borrow 后完成普通工作，再
读取最终结果；清理保护由编译器已有 drainer 承担，不用源码 defer 充当隐含交付点。
这只是物理消费者启用前的源码证明，不是公开 API 安全性验收。定向前端门禁通过，
仅恢复旧源码校验后，十个旧值/错误投影/提前调用负例均不再被拒绝。

初始化空槽也消费已有 callable effect/exec 事实，不能为了要求 initializer 是 plain
而新增 no-panic/no-suspend 标注：普通 typed pointer store 就可能保守包含 nil fault。
先把含指针的结果存储清零，再按已有 plain/outcome/coro 路径调用 initializer；只有
它正常完成后才注册资源临时 guard，之后才可开始 acquisition。初始化阶段尚未获得
资源，panic/cancel 走原调用者 cleanup，不对部分初始化槽调用 drop，也不新增一套
“部分初始化”的状态协议。initializer 的边界语义只允许建立空态，不能做资源 acquisition。

编译器内部守卫不等同于用户的 Go `defer`。它必须复用同一 drainer 的 join、panic
overlay 和 cancellation shielding，但不能让 drop 获得恢复调用者原 panic 的直接
`recover` 权限。drop 自己产生的 panic 仍可由它自己的 Go defer 恢复；用户原有 defer
的直接 recover 语义保持不变。没有源码 defer 的调用者没有 SSA `Recover` block，
共享 completion dispatcher 必须支持这种 owner，不能伪造恢复后正常返回的源码路径。
`TestCoroCleanupCompletionWithoutSourceRecover` 已验证该 dispatcher 的唯一 complete
case 与 invalid-continuation trap。统一 await 已把 owner 与 parent-recover 权限分开；
deferred builtin 的真实生成 helper 在 native/wasm32 的 CoroSplit 前后均不获得此权限，
恢复旧行为的负对照失败。新增 resource drop 的物理接线必须复用此路径。
隐藏 helper 若选择同步 outcome 入口，同样必须把已有 drainer 传给共用 completion
分派；panic/nil fault 替换 overlay 而不重置原有正常/取消清理 continuation。
对应 native/wasm32 前后 CoroSplit 门禁和仅恢复旧接线的负对照均已验证。

static 与 descriptor 调用消费同一个冻结事实、同一个结果槽和 cleanup 路径：

1. 按 Go 顺序求值 callee/参数，再初始化结果槽并注册临时 cleanup；资源动作此时尚未开始。
2. child strong join 后先消费 completion；cancel/非 Return 进入原 drainer，normal Return
   **只加载一次**用户结果，然后无 safepoint 地撤销临时守卫。不得先撤守卫再检查取消，
   不得重读已经 GC-retire/清零的指针结果槽。
3. drop 在调用 Close 前清槽/claim，随后复用普通 deferred-call 的染色、shield 和 join。
   新取消不能跳过 close 的 physical acknowledgement；close 错误也不能盲目重试。
4. 此类临时 cleanup 不加入循环 defer 的 heap 链。每个 parent frame 最多有一个尚未
   reconciliation 的普通 child transaction，可用单个 active-site tag 和 typed captures，
   在原 drainer 中先于此前 source defer 消费。drop 开始前清 tag；nested drop call 使用
   child frame。冻结计划必须证明这个不变量，不能另造资源取消状态机。

普通无资源 static call 和全部候选均无资源的 closed dynamic call 不产生 guard、tag、
descriptor tail load 或额外 frame 字段。open managed call 读取编译器注入的可选 recipe，
不从代码地址反查。函数值仍是 `{descriptor, env}`；拟采用现有 descriptor flags 指示的
可选尾部，普通 descriptor 保持原 base 大小。启用前必须一起更新/验证编译器 initializer
长度检查、ABI version、FFI 固定结构、MakeFunc 分配和所有 descriptor 消费者，不能只加
flag 就越界读尾部。当前 typed recipe/selection 是第一阶段，尚非这个物理消费闭环。

drop 的 typed 签名由 producer 的规范化签名机械生成：
`drop(out *ExactResultTuple, producerEnv unsafe.Pointer, args...)`，本身仍是普通 managed
callable，可 suspend。普通 FD drop 不需要原参数；静态/闭合集合可消去不用的 capture。
开放未知集合先保留必要 typed inputs，不为节省它们引入通用 heap context/capsule 框架。

内部 adapter 可借用当前 physical out slot，但必须由编译器验证准确类型和既有 borrow
协议；不能成为任意用户代码访问隐藏 ABI 的入口。结果被丢弃的 spawn 使用本地 fallback
槽，不解引用 nil out。纯转发推导仅接受精确 result projection 和无额外 effect/escape
的 helper 链；共享受保护槽，不先 claim 到一个裸 int 再返回。不接受清槽后又用缓存旧值
覆盖结果，或任意 recover wrapper 默认返回 `(0,nil)` 却被误认作成功取得 fd 0。

FD 0 是有效资源，而 Go 的路径校验失败可返回 `(0, error)`；因此公开 `(fd,error)` 的
drop 不能只测 `fd >= 0`，还必须按准确 adapter 契约检查 error。初始 fd=-1，错误路径
先关闭/撤销已取得的 custody，再发布原有 Go error 结果。`TestResourceDeliveryFDValues`
在私有子进程验证实际 FD 0、含 NUL 路径返回 `(0, EINVAL)`、重复 cleanup 与 fd 重用。
这里的值谓词测试仍不代表自动插入 cleanup 已完成。

`os.newFile` 的 fstat、fcntl、poll Init 和 SetFinalizer 也在保护范围内。raw fd 转为
`*File` 时必须向另一个预先保护的 typed slot 转移一次，再允许 finalizer 接管；不能让
raw-fd guard 与 File finalizer 同时认为自己拥有同一个描述符。成功交付普通 Go caller
以后，用户自己的 Close/defer 责任不再由本协议追踪。

#### 反射与库的交付边界

接收者求值快照使用结果 guard 自己的私有存储前缀：`{State unsafe.Pointer; Data ExactResult}`。
physical out 仍指向原有 `Data`，public tuple、funcval、G/P 和 operation ABI 不变；Data 为
零大小时保留尾部字节，使 out 仍是 allocation 内部指针。state 仅在已经证明的 pointer-to-value
method wrapper 中创建，保存终端 producer drop 所需的整组准确 typed arguments。原 primary 的
load、nil check 和求值顺序照常执行，之后在实际 acquisition 前发布 state。不能重新读取 *T，
也不复制 source CFG、建立 runtime binder/registry 或增加另一套取消状态机。

多层精确转发共享同一结果槽和最深层的 state，不重复快照或替换 binding。typed drop view
从 state 读取冻结参数；独立 archive consumer 不需要知道 producer 私有 state 布局。初始化
空槽或 receiver nil panic 期间尚无 state，`RequiresState` consumer 跳过 drop；完成 caller
acceptance 时清 state，取消路径则持有到原 cleanup drainer 的 helper join 完成后清除。
library v23、guard v5 与 delivery cold-tail v3 一起绑定这项保证，旧 archive/descriptor
不能被默认为已绑定。无 snapshot 要求的 producer 不分配 invocation record。

`reflect.ValueOf(syscall.Open).Call` 必须保留实际 target 的 recipe。只保护 ffi invocation
到 reflect 内部不够：现有 `Value.call` 在 invoke 后构造 `[]Value`，然后还需通过
`call/Call` 返回，存在第二交付窗口。有限 reflection bridge 要在 acquisition 前建立并
发布 Value result carriers，让它们指向同一 raw result cells；receiver 的准确 descriptor
recipe 负责解释/释放。inner/outer guard 清同一 custody cell，再 close，不能双重释放。
Call/CallSlice、method expression 和内部 `makeMethodValue` 的已知转发需全部保留该事实。

反射绑定方法保留原 `Value.Method` 的 receiver storage 与 method index，在实际调用时
求值，不能在 `Interface()` 时提前复制 addressable value、指针变量或 interface 的动态
接收者。求值后，实际 producer 的原生参数副本与资源结果由同一个 `ResultDelivery`
保留到最终 caller acceptance；之后修改 receiver storage 不影响取消清理使用的快照。
`go`/`defer` 通过相同的源函数入口执行这一步，nil output 使用已有 borrowed-result fallback。

编译器在 Method.Tfn_ descriptor 的完整 padded base/tail 之后，以独立 capability bit
发布可选的 receiver-free result prototype：准确 ABI hash、size/alignment 和独立校验的
typed initializer/drop view。真实 reflect 源码 helper 经已有 managed helper view 读取
public result 私有 State 所保留的固定 custody record，不新增调用引擎或地址反查表。
runtime 只为已知的资源方法或动态 interface 方法绑定该 contract；普通具体非资源方法
与任意公开 MakeFunc 不获得推断的资源所有权。interface 动态目标可以从资源 producer
切换到普通借用结果，实际调用没有 delivery recipe 时，取消不得关闭那个普通整数/fd。
library v23 将这项冷元信息纳入版本边界；旧 v22 archive 必须拒绝并重新编译。
void 与零大小返回值不携带 public resource prefix。已知方法 bridge 对这两类调用只用
自身的 nil-output fallback，不能把 public 空值地址当成固定 custody record 的输出槽。

公开任意 `reflect.MakeFunc(type, callback)` 不能仅从 type 或 callback 返回的 `[]Value`
推导资源所有权，其 descriptor 默认无 recipe。callback 若自行调用 Open，其结果已交给
用户 callback，后续由普通 Go 代码负责；这不同于 runtime-owned makeMethodValue 转发。
必须有普通 MakeFunc 返回 int/借用 fd 不会被擅自关闭的负例。

库沿用 `LibraryEffectFunction` 和现有 object/`__.LLGOCORO` 元信息承载可选 recipe、
initializer/drop symbol 和精确 ABI，更新 schema/digest，import 在冻结 call plan 前验证。
不存在第二个 resource manifest；导入方不得从 producer 未发布的能力推断一个 release。
缺 helper、错误 ABI/layout/version、claimed-safe 但 helper 链未保护均拒绝 publication；
普通显式无 recipe 不代表未知整数归资源。源代码不可见的 archive 必须分别通过 static、
funcval/interface 和 reflection 调用验收，DCE 不能删掉 live recipe 的 init/drop 引用。

#### 剩余完成 gate

纯数值、计数、errno、timer、普通 read/write 等不携带资源 custody 的结果仍使用
现有紧凑 scalar 路径，不增加 packet/G 大小、heap allocation、全局扫描或每次动态
release 分派。对可以静态证明无取消/跨 await 的边界允许消去守卫，但不能用
优化代替所有权证明。

实施顺序为永久 core oracle → 单一 typed recipe/validator → static/dynamic 共用消费及
descriptor/archive 闭环 → 有限 native public adapter → reflection/os wrapper → Node/WASI。
第一批不得在物理消费者闭环前启用 public API seed，不保留没有消费者的并行 custody 框架。
还须单独覆盖 `go acquisition(...)` 等丢弃结果的入口：当前 spawn 传 nil result sink，
没有普通调用者的接管守卫。fallback typed slot 的释放责任必须保持到真正的 terminal
Return 交付点；不能仅在源码 return 前设置 `done=true`，因为后续的 compiler poll
仍可能选择取消。调用结果临时守卫在源 defer 之前执行；入口自持结果守卫则在源 defer
之后处理失败出口，并且只在最后的正常结果发布路径撤销。两者仍使用原 drainer 和
completion 协议，不增加 G 字段或另一套资源生命周期状态机。该入口接线尚未实现。
以下强制门不变：

1. 正常消费、submit 前取消、Post 前后取消、物化后取消、迁移后取消均逐阶段验证。
2. wrapper 在 acquire 与 adopt 之间具有两个以上 await，分别在各点取消/panic；
   child 返回到 parent 后、parent 尚未取结果时取消也不泄漏。
3. select loser、duplicate callback、stale generation、fd 重用、错误结果、清理自身
  park/失败/重复请求均不 double close、不丢失 physical join。
4. native 真 fd、WASI out-pointer fd、Node callback fd 分别真实执行；检查 host
   handle 表与 fd 生命周期，不以 exit 0 或 host mock 单独证明平台完整通过。
5. 纯 scalar 编译产物与 frame 布局无额外成本，旧 source 可回收且 P-neutral 迁移
   不回访旧 owner；现有 cancellation/winner 语义和 raw boundary proof 不放松。
6. exact static、mixed funcval、interface、bound method、Call/CallSlice、内部 MakeFunc
   bridge 和源码不可见 archive 使用同一事实；错误 metadata 拒绝，不静默丢失 custody。
7. FD 0、错误 `(0,error)`、recover 默认值、Dup2 同 fd、fd 重用、finalizer 与 drop 竞争
   有明确负例；OS close 本身失败只能证明一次尝试和完成/error 已观察，不能称已保证释放。

## 4. Scheduler 与 operation contract

### 4.1 稳定对象

- `G`：逻辑 goroutine，拥有 frame chain、scheduler state、一个当前阻塞 wait cell和取消/抢占状态。
- `Continuation`：opaque backend handle，只允许 scheduler driver执行 `resume/done/destroy`；当前实现由 LLVM coro提供。
- `OpID`：只含 source、slot和generation的标量 identity，可跨线程、host callback或IRQ handoff。
- `OperationRecord`：source-owned稳定记录，保存 owner G、result、取消状态以及必要的GC roots。

平台 producer只能持有 `OpID`或target自己的POD token，不能持有裸 LLVM handle、临时 frame地址或无owner的 Go pointer。

### 4.2 Operation lifecycle

逻辑等待与物理event source是两个关联但不能混合的状态机。`select`会让一个逻辑等待关联多个物理source handle；外部producer的生命周期也可以长于已被唤醒的G。

```text
logical WaitOwner / G ParkState
  Idle -> Armed(ticket) -> Claimed/Parked
       -> Completed | Canceled -> Consumed -> Idle(next ticket)

physical ParkSource slot
  Free -> Active(handle) -> Delivered | Closing
       -> Detached -> Quiesced -> Reusable(next generation)
```

`BeginParkSet -> Attach* -> Seal -> PrepareParkSet/Commit`是一段短小的owner-P preparation transaction。期间不得抢占、spawn、切换frame或提交另一种suspend transition；任一步在producer admission之后失败，都必须在返回用户代码前执行`AbortParkSet`并沿同一resolution/detach协议释放已发布资源。该no-preempt约束只包围元数据提交，不包围实际I/O等待或可能阻塞的host调用。

`WaitTicket`只标识一次G的逻辑park；`OpID`只标识一个物理source slot。两者不合并，也不把Go指针编码进identity。对外ABI在未验证所有32-bit目标的alignment之前，`OpID`保持显式的两个`u32` POD word，不直接依赖Go `uint64`布局。

关键不变量：

- Arm 在 operation 对 producer可见之前完成，early completion不能丢失。
- Park 只把一个 exact generation绑定到一个 G。
- Complete 与 Cancel只有一个 terminal winner。多候选等待的claim结果必须能区分`Won`、`Lost`和`Invalid`，`Lost`不是runtime corruption。
- `DetachWaiter` 在 G重新进入 ready queue前清除 source 对 G/frame/wait cell的全部访问能力。
- `RecycleSourceSlot` 只有在 producer unregister/join 或其他strong quiescence后才能复用 slot generation。
- Timer 没有外部 producer，due drain只发布completion fact；统一resolution负责winner/loser detach。只有resumed wrapper已对winner result lease执行Take/Discard（或取消路径未产生lease）后，该physical generation才能recycle。
- fd/host/worker operation可以先形成 pointer-free tombstone，等待backend quiesce后再recycle。

一个 G 同时只会因一个逻辑 wait进入 `GWaiting`。稳定G中应内嵌完整`ParkState`，而不只是一个临时`WaitToken`；它至少包含ticket、phase、outcome和wait-set reference。logical ticket使用两个显式`u32`的epoch/generation，只在完全consumed且所有source已detach后递增，epoch耗尽时fail closed，不能主动回绕到旧identity；ticket不进入producer ABI或跨线程cancel queue。

`select` 可以注册多个source candidate，但它们共享同一个 G-owned winner cell；loser在winner确定后取消并完成detach barrier，之后winner才可以ready。Detached/background operation使用独立 operation record，不占用 G 当前 wait cell。

### 4.3 多候选 select 与执行取消

这两项是scheduler/operation core的基础能力，不是netpoll或某个API的特例。

`select` 使用一个稳定`WaitSet`：

- 一个owner G、一个logical ticket和一个owner-P winner cell；producer只发布source fact，不直接竞争或修改winner；
- 多个candidate，每个持有独立`OpID`、case index、result record和detach phase；
- ready的candidate只尝试claim winner，不直接唤醒G；
- 败选candidate返回`Lost`并进入cancel/detach，不得当作stale/corruption；
- 所有loser达到detached或pointer-free tombstone后，executor才把winner对应的G放入ready queue；
- 已具备多个ready case时，在不破坏Go伪随机选择语义的前提下选winner，不由source扫描顺序偷偷决定。

这里的V2 `ParkState`首先覆盖timer、I/O、host、worker和IRQ等“完成事实一旦发布就可提交”的多事件等待。完整Go channel `select`还多一层语言契约：channel和send右值只求值一次；nil case被禁用；只有没有通信可提交时才选择`default`；closed receive、closed send panic以及当前所有可执行通信之间的uniform pseudo-random selection都必须保持。event-ready snapshot只能提名candidate，channel candidate必须在channel同步域内执行原子`TryCommit(ticket, case)`；若状态已变化则继续尝试本轮其他candidate或重新park。当前typed `hchan`已与compiler lowering和runtime commit-domain接线，已覆盖无缓冲send/receive、缓冲fast path、multi-case `select`、close和send-closed fault；但nil/default组合、uniform random的统计性质、全部close竞态、timer channel联动以及当前Go GOROOT channel/select全矩阵尚未完成认证，因此这是已接线的核心能力，不是完整Go `select`兼容性结论。

每种candidate在catalog中固定一种commit contract：`ReadyThenTryCommit`只提名ready并在自己的同步域提交（channel）；`Reservable`先取得可回滚reservation，winner提交、loser退回；`IrreversibleCompletion`表示副作用已经发生，只有result允许明确discard时才能参加多路等待。resolver只处理这些统一的claim/disposition，不尝试为任意I/O伪造事务回滚。

Operation result ownership使用单字节显式状态，而不是两个可组合出非法形状的boolean：`Empty -> Owned -> Leased -> Taken|Discarded`是winner路径，已完成物理cancel/rollback的loser可执行`Owned -> Discarded`。`IrreversibleCompletion`和`Reservable`只有成功publication才建立`Owned`；`ReadyThenTryCommit`的ready hint始终保持`Empty`，source先用exact request做pre-effect gate，在同一个owner-serialized、不可重入握手中完成物理effect，再由唯一bind入口建立`Owned`并生成success attempt，不能从request直接构造未绑定的success。loser source必须先做真实cleanup/rollback，再`Owned -> Discarded`，之后才能ack；winner只有在`ConsumeParkSet`时`Owned -> Leased`，resume/cleanup分别显式`Take`或`Discard`。winner仅`Taken|Discarded`可recycle，loser仅`Empty|Discarded`可recycle；stale ticket、重复bind、重复Take/Discard全部fail closed。

固定标量result由需要它的source slot内嵌公共`ScalarResultCell`，而不是扩大所有G或operation。V1 payload是28-byte/align-4 POD：`Meta uint32; Words [6]uint32`；Meta编码version/kind/logical-count/physical-word-count/flags，V1接受0..3个逻辑`uint64` scalar，每个值固定编码为`low32,high32`，未使用word必须为零。cell总计36 bytes并绑定exact `OperationID` generation；winner读取还要同时匹配`OperationResultLease` ticket。异步producer不能并发写这个普通cell：它先写source-specific atomic mailbox并release fact，owner acquire-drain后在publication前复制；Ready source则在exact gate后的物理effect与bind握手中stage。Take先复制POD到局部，再完成通用Take，最后清cell并向调用者公开副本；winner/loser Discard先释放cell再改变ownership，失败时恢复旧cell，因此stale或duplicate capability既不泄露payload也不能误清新generation。Timer等无payload source不内嵌cell，Manual producer ABI保持原两字`OperationID`。
Phase 32 C0把真实Channel commit-domain接入这套core，但刻意不提前实现typed `hchan`：同一select的所有channel registration共享一个4-byte、4-byte-aligned `SelectClaim`，状态为`Open -> Acquiring -> Committing -> Claimed`。owner resolver在rank scan前以CAS取得`Acquiring`并持有到Pending回退或terminal publication；select-to-select peer在hchan同步域内按稳定顺序先取得两端exact-ID admission，再访问两端frame claim。外部提交严格执行“两端admission held -> 只验证lifetime-stable的slot.claim/generation映射 -> 两端claim Open到Acquiring -> 在claim排他下验证两个owner-only record/link exact identity、Pending disposition/candidate和exact parked ticket -> `BeginEffect`把两端Acquiring CAS为Committing -> typed physical/result -> 两端sticky mailbox -> 两端claim release-store Claimed -> checked release两端admission -> 两端executor request/doorbell”。caller-owned fixed-layout pair transaction保留原始endpoint A/B映射，只用stable source-slot address决定admission顺序；调用方把零值out-param传给`BeginPair`，transaction保存`self == out`的地址身份。`BeginEffect`、`AbortPair`、`CommitPair`和内部release在触碰任何claim/slot前都先验证该身份，所以按值副本即使在原对象`BeginEffect`前调用Abort也不能释放真实admission；pair的`self/phase`才是线性身份主证书，checked release只拒绝aggregate count已经为零的fail-closed错误，不能在另一个合法producer仍admitted时单独证明某个lease未被复制。普通失败把out恢复为零，只有fail-closed Broken保留self与尚存lease。C0的Go gc测试确认self pointer会使普通caller local逃逸，因此“LLGo coroutine frame不移动”本身还不等于production零分配证明：C1真实hchan接线前，compiler contract test必须同时证明out storage位于不移动的coroutine frame、不产生heap allocation，并证明从双admission到commit/release的整个hchan临界段为NoSuspend/NoPanic；任一证明缺失都禁止接线。调用还必须受hchan同步域串行化，不能并发操作同一原对象。`BeginPair`完成双admission、稳定映射验证、双claim和claim内record验证，`BeginEffect`取得共享且不可回退的effect permission，并用`Committing`让resolver看见不可回退的外部提交窗口；effect后只有`CommitPair`能发布两mailbox/两Claimed并release，`AbortPair`仅允许pre-effect。admission只保frame lifetime，不与owner resolver的record mutation互斥；因此外部matcher禁止在取得claim前读取record。任一admission失败时尚未触碰claim；claim争用失败时必须在admission仍held时rollback claim再release，release后禁止再访问frame。claim后的record核对失败也必须先rollback双claim再release双admission；任一步异常、第二个Committing CAS异常、post-effect Duplicate或其他不变量错误都不能当幂等成功，而要保留全部lifetime lease并fail closed。Apply在closed-with-zero后若frame claim仍不是Claimed，说明不是正常的外部Acquiring/Committing（后者本应仍持admission），必须在detach/clear前Invalid fail closed。source producer ABI仍只保存`OperationID`；未来hchan queue node保存的claim pointer完全受这两个admission lifetime lease覆盖。effect之后才acquire admission或mailbox之后提前release都不安全，禁止提供这种convenience；已经取得的admission即使随后遇到source Closing，也必须完成exact forced/claim publication再显式release。

Phase 32 C0在标准host-Go下因`self` pointer观察到的逃逸只是test artifact，不是production allocation contract。C1真实`hchan`必须由compiler提供caller-owned pair storage，并以noescape/frame certificate证明该storage位于不移动的coroutine frame且无heap allocation；还必须独立证明整个`hchan` critical section为NoSuspend/NoPanic。

`Ready`和`Forced`不是同一个mailbox状态。若peer在owner已把`Ready`改为`DrainingReady`后提交，producer单调写入`ForcedBehindReadyDrain`，owner结束旧drain时必须转成新的sticky `Forced`，不能要求不可逆producer重试；若producer在owner读取`Ready`但尚未CAS `DrainingReady`时先改成`Forced`，owner CAS失败后必须重读并在同一slot visit改为`DrainingForced`，不能把正常竞态当corruption。若forced mailbox落在epoch A的Channel cursor之后，claim已是`Claimed`但当前owner record仍不是forced，resolver在进入`ParkState.resolving`前按原顺序恢复整个未处理affected FIFO并结束A；B或下一transaction先发布exact forced record，再解析它，不能在resolver内部等待自己。外部forced胜过rank、default和ordinary operation cancel；只有Abort/Shutdown可抑制continuation，此时candidate保持`Committed`，result必须由source执行`Owned -> Discarded`，绝不能伪装成`RolledBack`。

Channel `TryCommit`或discovery看到外部`Acquiring/Committing`时返回`RetryBudget`不是semantic failure，但也不能把未完成resolver跨host钉在同一source step。owner保持Ready hint/readiness generation不消费，若已进入commit则用exact request执行`abortParkSnapshotCommit`并释放自己取得的claim，然后按原顺序恢复未处理affected FIFO、完整结束本epoch并返回`more`；A/B最多各尝试一次，下一次transaction前runner可先偿还ready debt，让持有hchan锁/peer admission的G继续运行。这对应Rust `Pending`让出executor和BEAM reduction yield，而不是本地busy retry。Apply先seal admission；若仍有held producer则返回`RetryBudget`并保留link/source claim pointer，只有closed-with-zero join后才允许discard/ack/detach/clear frame pointer和promote G。backend strong join、late mailbox分类、physical cleanup和`ConfirmQuiesced`仍独立于detach，winner lease结束后才能Recycle。claim保持`Claimed`直到同一select的全部channel registration均detach且source不再保存claim pointer，再由resume/compiler owner显式重置为`Open`，不能由某个loser slot提前重置。

C0的`ChannelOperationSource`只是固定4槽、无payload的source/catalog/route skeleton：`OperationSourceChannel`追加在已冻结的Control值之后，producer ABI仍只有两个`u32`的`OperationID`；`G/P/ParkState/WaitSetRecord/OperationRecord`均不增长。claim-less的单个真实channel operation可以走owner `ReadyThenTryCommit`，但C0拒绝其external peer admission；C1必须先为单case hchan加入与claim等价的physical-committing fence。C1还需完成typed send/recv queue、buffer/close/nil/panic、payload GC rooting与copy、两端route ingress、uniform permutation、compiler lowering和完整GOROOT channel/select测试。C0只依赖对齐的32-bit load/store/CAS与静态owner storage，因此同一个claim/resolver核心适用于native、WASM、RTOS和baremetal，不依赖libuv、BDWGC、pthread或OS锁。

取消是分层协议，不是一个boolean：

1. `CancelRequested`：已将请求durable publish，但completion仍可能已经获胜。
2. `LogicalCanceled`：logical wait/wait-set已选定cancel outcome，G仍不一定可ready。
3. `Detached`：source已不再能访问G、frame、winner cell或Go result pointer，此时才可ready/reclaim G。
4. `Quiesced`：backend已unregister/join，旧callback不再可能进入，此时才可复用slot generation。

Completion与取消必须竞争同一terminal ownership；已经完成的syscall副作用不能被“取消成功”追溯撤销。Go语言没有安全的任意goroutine kill语义：对running G的取消只发布请求，在compiler验证的safepoint或park boundary观察；标准库operation通过`context`、deadline、close或返回error传递取消。只有明确的runtime shutdown/Goexit策略才能终止任务，且必须遵守defer/panic展开语义，不能直接丢弃continuation frame。

这里采用其他语言和系统已经验证过的最小机制，不复制它们的表面API或对象模型：

| 参考模型 | 采纳的机制 | 明确不采纳 |
| --- | --- | --- |
| Go [`select` spec](https://go.dev/ref/spec#Select_statements)与[`runtime/preempt.go`](https://go.dev/src/runtime/preempt.go) | G/M/P分离、同步/异步safepoint、netpoll wake、没有远程kill；channel select保持一次求值、原子通信提交和uniform pseudo-random选择 | 不照搬stackful G stack、runtime内部channel锁结构或依赖特定OS的async signal抢占 |
| LLVM/C++20 coroutine、[`stop_token`](https://eel.is/c++draft/thread.stoptoken)与[sender/receiver operation-state](https://eel.is/c++draft/exec) | coroutine只提供frame/continuation；`connect/start`后的operation-state活到唯一terminal signal；取消是单调cooperative state；completion scheduler与operation分离 | sender模板/type-erasure对象图；stop callback可在`request_stop`或注册线程同步执行，甚至令注销等待callback；llgo的foreign thread、host callback和ISR只能publish fact与doorbell |
| Rust [`Future/Waker`](https://doc.rust-lang.org/std/future/trait.Future.html)与Tokio | wake保证未来至少一次poll，重复wake可在已入队状态下合并；reactor/executor分离；drop不等于backend quiesce | 把`Future/Poll`变成Go ABI或标准库编程表面，以及把drop当成I/O已经detach/recycle |
| Swift [structured concurrency](https://github.com/swiftlang/swift-evolution/blob/main/proposals/0304-structured-concurrency.md)与[checked continuation](https://github.com/swiftlang/swift-evolution/blob/main/proposals/0300-continuation.md) | suspension、parallelism与executor affinity分离；cooperative cancel flag；continuation必须exactly once resume | 假设普通suspension自动抛取消；cancellation handler可并发立即执行，不能成为llgo requester线程直接运行cleanup的先例；也不为普通`go f()`强制建立完整Task对象树 |
| Kotlin [`suspendCancellableCoroutine`](https://kotlinlang.org/api/kotlinx.coroutines/kotlinx-coroutines-core/kotlinx.coroutines/suspend-cancellable-coroutine.html) | 与`CoroutineDispatcher`协同的prompt cancellation：ready但尚未执行时仍可转入cleanup，同时保留`onCancellation`结果资源清理责任 | 把该保证泛化到任意interceptor；每G常驻`Job`、`CoroutineContext`、异常对象和callback链 |
| Java [virtual thread](https://openjdk.org/jeps/444)与interrupt | 保持同步阻塞调用风格；逻辑G与carrier M分离；各operation定义取消后的error/close语义 | stackful heap stack、可清除interrupt flag、`Thread.stop`以及把Loom误当成公平time-slice抢占 |
| C# async、`CancellationToken`与[`IValueTaskSource`](https://learn.microsoft.com/en-us/dotnet/api/system.threading.tasks.sources.ivaluetasksource-1) | cooperative cancellation、同步完成fast path、opaque version token、可复用operation source和单次结果消费 | 默认`Task`对象ABI、隐式ExecutionContext捕获、同步取消callback和用异常承载runtime core状态 |
| Haskell [`STM/orElse`](https://hackage.haskell.org/package/stm/docs/Control-Monad-STM.html)与`async` | 多路等待先组合再原子commit；`mask/bracket`把取消交付与资源cleanup分离；调度budget必须覆盖立即ready路径 | 异步异常注入任意用户点、STM retry log、lazy runtime或heap stack成为Go ABI |
| Ada [selective accept](https://www.adaic.org/resources/add_content/standards/05rm/html/RM-9-7-1.html)与CSP/[core.async `alts!`](https://clojure.github.io/core.async/clojure.core.async.html) | 多候选只提交一个alternative，timeout/default与普通候选共享明确选择点；败者registration必须撤销 | stackful task/rendezvous runtime、异步transfer-of-control或每次select创建channel handler对象图 |
| JavaScript Promise与`AbortSignal` | abort state、通知与physical completion分离；host callback只带generation token | abort listener可在`abort()`中同步执行，llgo仍只允许publish fact；不用`Promise.race`实现Go select，不让microtask直接resume G；Promise loser默认继续运行，不能代替detach barrier |
| Python [Trio cancel scope](https://trio.readthedocs.io/en/stable/reference-core.html) / AnyIO | cancellation是level-triggered sticky状态，只在checkpoint交付；deadline、嵌套scope和shield是可组合的控制结构；cleanup可继续await | 用异常承载runtime core状态、每层调用动态分配scope/context，或强制普通`go f()`进入结构化task tree |
| [OCaml 5 effect handler](https://ocaml.org/manual/effects.html)与Eio switch | continuation是one-shot并只由handler/executor恢复；显式switch可收口child与resource lifetime | multi-shot/clone continuation、source/waker直接resume LLVM handle，或引入通用algebraic-effect ABI |
| [Dart isolate/event loop](https://dart.dev/language/concurrency) | callback只投递event并合并requestRun；一个executor串行拥有scheduler状态，适合WASM/embedded host re-entry | 每G一个isolate、Future API污染Go表面、microtask无限优先造成I/O/timer饥饿，或hard kill替代defer unwind |
| [libdispatch/dispatch source](https://developer.apple.com/documentation/dispatch/dispatchsourceprotocol/setcancelhandler%28handler%3A%29) | event data merge、serial owner affinity、wake coalescing，以及cancel-handler所表达的source生命周期确认 | 每operation一个block/queue、`dispatch_sync`重入，或把source cancel等同于logical cancel/backend quiescence |
| Erlang/BEAM | reduction是VM work-unit/safepoint上的有界cooperative preemption；per-scheduler run queue、global rebalance/work stealing可作为multi-P参考 | 把reduction误写成任意LLVM指令上的强制抢占或墙钟时间片；每G mailbox、selective receive、exit-signal强杀和消息复制隔离 |
| RTOS/baremetal event loop | ISR只写固定POD slot/ring、sticky bit并通知executor；result写入/release publish与owner acquire drain配对；generation先校验再访问结果；静态容量、明确溢出策略和one-shot alarm | 用`volatile`替代happens-before；每G一个RTOS task、ISR分配/加锁/访问Go pointer、每operation一个event-group object |
| Zig/freestanding工程约束 | 显式allocator、无隐藏线程、target capability与确定性allocation failure | 不把它当作成熟异步模型，也不依赖Zig的语言级coroutine ABI；该能力并不是可供llgo复用的稳定契约 |

这些模型共同支持一条轻量流水线：producer只发布`OpID`对应的sticky source fact并触发可合并doorbell；owner P按有界publication epoch完整访问所有source，再扫描受影响的wait-set；按预生成随机rank选择winner；source对loser执行detach或生成pointer-free tombstone；最后才enqueue G。初期实现可以扫描P的waiting集合验证正确性，但最终高并发实现应由source记录affected wait-set，不能把每轮`O(全部parked G)`冻结成长期契约。

由这些参考模型得到的公共约束是：

- one-shot的单位是一次suspension epoch的`ResumePermit(frame, epoch)`，不是整个LLVM coroutine frame；同一frame可以跨多个epoch反复suspend/resume，但每个epoch的resume或destroy权只能消费一次，destroy使全部旧permit失效；
- operation的logical terminal、ParkLink detach、backend quiescence和storage recycle是四个不同阶段；完成可以携带普通值或error payload，task stop则转入cleanup控制流，不能用一个`done`位混合；
- cancellation是durable单调事实，只能在safepoint、park boundary或resume prologue claim；claim后冻结本次cause，cleanup/defer允许再次park且不会被同一请求反复打断；
- cancel registration必须完成`reserve/attach -> 观察sticky cancel -> backend admission -> 再确认`的无缝握手，或由backend提供等价原子register；cancel发生在任何窗口都不能遗漏，也不能在loser detach前释放result ownership；
- 每类operation必须声明取消强度：不可取消、仅阻止启动、cooperative或best-effort physical cancel；已发生的syscall/I/O副作用不能追溯撤销；
- 每个select candidate还必须声明commit模式：`ReadyThenTryCommit`、`Reservable`或`IrreversibleCompletion`。不可回滚且loser副作用不可接受的operation不能伪装成通用select candidate；Go channel只在channel同步域`TryCommit`；
- backend `Start`只执行一次，inline同步完成仍只能publish一个terminal fact，不能递归resume、Poll或运行cleanup；value、普通Go `error`、panic/Goexit/task-stop必须保持不同payload或控制流分类；
- 每个source静态声明mailbox合并代数：one-shot、OR、max、saturating-count、replace-latest或bounded queue；容量耗尽必须同步失败、背压或发布可观测overflow fact，不能静默丢失；
- structured scope是按API显式创建的可选对象；scope close需要等待child terminal和其source quiescence，普通`go f()`不为此常驻父子树；
- 每个P对control、timer、I/O、host和worker source采用有界公平drain；连续producer不能阻止已经claim的epoch进入resolve/promotion，也不能复制JS microtask或高优先级dispatch source无限压制其他source；
- 调度budget覆盖所有取得进展的路径，包括立即ready wrapper、连续child await、runtime helper、source batch和ready task连跑，而不只compiler loop backedge；超长不可切分helper必须转有界worker；
- executor禁止同线程递归re-entry，也禁止两个M同时拥有一个P；同步`requestRun` callback只置pending后返回，WASM/embedded slice若返回`more`必须安排下一次host entry；
- 同步完成保留allocation-free fast path；只有真正跨线程、跨host或开放lifetime的边界才分配`{slot,generation}` endpoint。

基础G因此只保留`TaskCancelKind`和`Idle -> Requested -> CleanupClaimed`的轻量phase，复用现有preempt/park/SourceSet wake路径；claim后冻结terminal cause，cleanup/defer内可以再次park而不会被同一请求反复取消。Go本身没有任意goroutine handle，不为每个G常驻外部handle registry。`context`、I/O和host取消仍是普通`OperationID`事件。`Goexit`是当前G同步进入cleanup的独立compiler控制流，不是可向其他G注入的task cancel kind。production `TaskControl`端点已实现，但只在明确的host/export边界按需分配generation端点；普通G仍不常驻外部registry，当前也尚未暴露完整的公共/标准库级task cancellation API。

显式host/export task handle使用固定容量`TaskControlSource`，其producer ABI仍是两字`OperationID`。producer只把`Abort/Shutdown`按强度单调合并到原子mailbox，再走公共executor request/doorbell；owner P每轮对每个slot最多取一个合并事实，因此高频control请求不能饿死timer、I/O或IRQ source。endpoint close先seal admission，close前已经接受的fact仍必须交付；task已经terminal时才作为正常late fact丢弃。generation只有在所有已进入producer返回、final drain完成且owner清除G指针后才能复用。这相当于采纳`stop_token`的单调状态、Trio的checkpoint交付和dispatch source的cancel/quiescence分离，但没有同步callback、每G对象树或foreign-thread cleanup。

`RegisterTaskControl`第一次接收任意`*G`时仍执行完整ready/wait队列审计；注册成功后，source delivery才可用exact slot、`source.owner`和非零`taskControlLeases`证明owner。Running/Dispatching校验`P.current/runP`，V2 Waiting校验exact frame-local `WaitSetRecord`，Runnable和legacy Waiting校验全部task-local状态并依赖当前single-P“注册lease存续期间禁止迁移”的不变量。park candidate链已由prepare/seal/owner transition审计，registered mutation只复核scalar header、local head和winner record再设置sticky cancel，不重走远端`ParkLink`；因此每个已注册slot的事实交付为O(1)，不会遍历无关ready/wait tail或candidate tail，后续logical resolution仍按已有逐candidate reduction执行。公开`RequestTaskCancellation`、`TaskCancellationOf`和`ClaimTaskCancellation`面对任意G时继续执行完整队列/park审计。未来multi-P在迁移G之前必须原子transfer control lease locator，或把请求forward到旧owner后再迁移；不能直接沿用single-P证明，也不能为此给每个G增加P指针/对象。

`ParkReady`不等于selected continuation已经开始执行。为兼容Kotlin所谓prompt cancellation但不引入其Job/exception对象，LLGo在每个P保留一个瞬态`RunDecision`槽：`PollReady`只把完成detach barrier的G移入ready queue；scheduler在返回`ActionResume`前消费ParkState、claim task cancellation并发布ticket/outcome/case/result lease；compiler生成的resume prologue必须先取走exact ticket的decision，再复制或丢弃winner result并选择普通continuation或cleanup。未取走、ticket不匹配或重复取走均fail closed。decision在P上按执行资源计费，不给每个G增加常驻结果字段；编译期布局预算将`ParkState`锁定为64-bit 56 bytes/32-bit 48 bytes，将`RunDecision`锁定为64-bit 40 bytes/32-bit 36 bytes。

运行中的G若在本次resume gate之后才收到task cancellation，request保持sticky，到下一合法safepoint或park boundary再claim。`FrameComplete`、panic和未来Goexit等不可恢复terminal suspend不得绕过尚未claim的`Requested`；compiler cleanup lowering完成前，runtime必须对这种形状fail closed，不能先销毁frame再留下永远无法acknowledge的cancel token。

## 5. Event source 与 executor contract

### 5.1 Event source

Event source概念上提供以下 owner-side能力；实现不要求使用 Go interface，可由静态 source table、generated ops或目标特化函数实现：

- `Publish(now, budget)`：在pass入口capture本source当前可claim的有界prefix/slots，把事实sticky publish到source-owned `OperationRecord`；未claim或并发到达的事实保持`Pending`留给下一epoch；
- `ResolveAffected()`：只能在完整SourceSet drain barrier之后扫描受影响wait-set并决定logical outcome；
- `NextDeadline()`：返回最早绝对 monotonic deadline；
- `Cancel(OpID)`：竞争或发布取消；
- `Detach/Quiesce/Recycle(OpID)`：分离 waiter与物理source生命周期；
- `Pending()`、`Empty()`：idle和shutdown验证；
- `BeginClose/ConfirmClosed()`：阻止新producer并strong join。

Source-specific submit保留在各自模块，但成功后必须返回统一 `OpID`并遵循上述生命周期。

### 5.2 SourceSet

每个 P/executor绑定一个冻结的 `SourceSet`。Executor不得在主状态机中写 `if timers != nil`、`drain waits then timers` 之类source分支；当前手写静态catalog后续应由target profile生成direct calls，而不是引入interface dispatch。公共scan结果只包含：

- completion数量；
- 是否产生 runnable G；
- 最早 deadline；
- pending/requested/invalid状态。

引入第三种 fake source时，compiler不变，executor idle/shutdown算法不复制，只增加source实现和SourceSet注册。这是第一项结构验收。

不设置中心化completion fact容量。`OperationRecord.completionPublished`本身是durable fact；所有source完成一个有界publication epoch后，再由各source枚举本轮affected operation并调用同一个park resolver。多个candidate指向同一ParkState时，第一次扫描本epoch完整sticky snapshot完成决策，后续重复项看到已进入detaching phase即可跳过。因此epoch开始前已durable的winner不依赖source顺序，也不需要每P固定大数组、batch overflow或全局transaction rollback；epoch进行期间并发到达的事实允许本轮或下一轮处理。

高并发promotion使用直接park物理协程frame内的临时`WaitSetRecord`，不为所有G常驻增加`prevWait`或affected link。record包含owner G、exact ParkTicket、active-wait双链、affected work link/state，以及一个只在同一frame内解释的`unsafe.Pointer + resumeBindingKind`联合槽；64-bit为56 bytes、32-bit/WASM为32 bytes。联合槽只能是none、单source packet、固定枚举typed cleanup plan或已物化packet，不保存interface/function value。active双链允许ready wait-set在O(1)内从P移除，per-P affected FIFO在每个published epoch结束时切成线性batch；同一wait-set的多个source fact通过`clean/queued/processing/dirty`状态合并。bootstrap或无法由compiler提供frame slot的入口使用调用方提供的静态pool，且必须在任何producer admission前reserve；native profile可选择可增长pool，baremetal/RTOS必须显式声明静态容量和同步失败。

该结构只让同时parked的任务付费，并保持producer/ISR仍只处理两字POD `OperationID`。迁移阶段legacy与V2各保留一对active head/tail，P另有affected head/tail，`Frame`暂存一个record pointer；legacy删除后应让`G.nextWait`原位承担当前record入口并合并这些队列字段。V2 fact mark、affected pop、promotion以及record-aware attach/detach已经只做O(1) header/邻接preflight，完整审计保留在测试、debug和terminal边界；`ParkLink.previous`由同时parked的source-owned operation支付。目标复杂度是`O(F + A + C)`：本轮source fact数F、受影响wait-set数A以及这些wait-set的candidate数C，与其余parked G无关。

### 5.3 防丢唤醒 idle transaction

所有平台执行相同协议：

1. Active poll执行有界epoch A：完整访问一次SourceSet，把各source本轮claim的producer mailbox转成sticky operation fact，随后立即统一`ResolveAffected -> Apply/Detach -> Promote`。
2. Acknowledge coalesced executor request。
3. 无条件执行同构的有界epoch B，然后本次Poll返回；B结束时即使仍有pending/request，也只表示下一次Poll仍需服务，不能循环等待producer静默。这样持续producer不能饿死A已经claim的wait-set。
4. A前已durable但其request在ack前被coalesce的fact必被B的完整catalog pass看见；ack后到达的request保持sticky。epoch中未claim的fact仍留在source mailbox，不依赖瞬时全局快照。
5. 检查local ready、global injection和waiting状态；确实需要阻塞时才发布`IdleArmed`。
6. `IdleArmed`后无条件final Publish完整SourceSet，但不在idle gate中resolve。若发现工作，先离开idle gate，再重新执行active的A/ack/B transaction；其A会解析idle final pass已经发布的affected batch。
7. 若仍无工作，按最早deadline执行`CommitSleep`。
8. Platform wait返回后先离开idle gate，再执行完整active poll。

Doorbell是通知，不是事实源；即使通知被coalesce或出现spurious wake，事实仍在source table/completion queue中。

### 5.4 Service budget 与 `more`

一次executor entry接受确定性的reduction budget，不用墙钟时间猜测公平性。至少以下动作计费：source slot/ring item、claimed fact、affected wait-set、candidate apply/detach、G dequeue/resume/destroy、立即ready wrapper和child await。epoch A与B仍各自完整访问静态catalog一次，因此target必须提供不小于`MinPollBudget`的slice；dynamic source只claim固定quantum并保留cursor，不能把“扫描全部容量”作为长期API。

Select winner决策是不可拆的原子工作单元，允许在声明的`MaxSelectCases`内有界overshoot；winner确定后的loser detach可以分批，但barrier归零前不能promote。source若只扫描mailbox前缀，必须用cursor/sequence或ready-index ring保证先清pending不会丢掉未扫描事实。

`RunSlice`返回`{status, used, more, blocked, nextDeadline}`。`more`是必须再次调度的义务，不是递归调用许可；budget耗尽、ready/injection队列非空、source/affected/detach backlog、request仍sticky、deadline已到或DriveAdmission存在deferred entry都设置`more`。`blocked`则表示当前只缺新的external fact，例如backend cancel acknowledgement或physical quiescence；它不能同时因为同一个operation设置`more`，否则`OperationApplyDeferred`会形成无事件忙转。内部apply结果因此要区分`RetryBudget`与`AwaitExternalFact`，而不是用一个笼统的Deferred覆盖两者。Native worker可在同一个固定scheduler stack外层迭代；WASM/embedded必须安排新的host entry后先返回；RTOS/baremetal只置notification或让下一main-loop iteration跳过WFI。同步`requestRun`、completion callback和IRQ永远不能因为`more`直接重入executor。

## 6. 并行模型

### 6.1 逻辑映射

- `G` 是可调度任务。
- `P` 拥有 runnable queue、source shard、timer shard和调度预算。
- `M` 是实际执行上下文，例如native线程、RTOS task、WASM host re-entry或baremetal core loop。
- M必须取得P后才能运行managed G；一次只有一个M拥有某个P。

Running和Waiting G不可迁移，completion必须投递原owner；只有已经清除source-affine状态的Runnable G可在P间steal或通过global injection迁移，G被steal后从下一次operation开始才绑定新P。Pinned/ThreadAffine G使用固定M/P协议，不能退化成全局TLS猜测。

当前native fleet profile已经把这条core接入真实程序：route 1原位收养command线程上的program P，其余7个固定route各由一个joinable pthread M拥有；每个domain有独立P/driver/source catalog、pipe doorbell和POSIX poll set，并执行同一个物理reducer。完整有界topology在首个managed resume前建立，关闭时统一stop、逐M join、逐route strong-close，route/source/event identity在进程生命周期内不因并行度变化而重建。启动`GOMAXPROCS`环境或online CPU数量只初始化逻辑execution quota，标准`runtime.GOMAXPROCS`在运行期调整允许同时进入managed resume的数量。C叶只有一个静态C→Go owner边界，pthread参数只是稳定`uint32` M slot，不传函数地址、G、P或coroutine handle。固定8槽`RunnableTransferMailbox`接受never-run root、普通`SuspendYield`以及已进入`parkMaterialized`的`SuspendPark` continuation；slot以generation和FIFO exact import持有唯一GC root。G复用既有对齐padding中的一字节`transferState`，Published期间关闭pointer-only preempt gate并拒绝普通`Enqueue`，因此不会被第三个P重复取得，且native/wasm32的G大小不变。

全局注入现在使用同一mailbox上的demand-driven work sharing，而不增加第二条G队列：空P只发布无指针的`Idle -> Requested`需求，源P在一次stable physical action后用`Requested -> Claimed`取得唯一服务权，并把一个P-neutral runnable连同exact executor request发布给该route。通用surplus路径中的普通yielded/materialized continuation只有在源队列至少有两个runnable时才会转移，始终给源P保留一个；never-run initial是该通用路径唯一允许单独转移的例外，以避免spawn后立刻进入无safepoint计算时饿死空P。目标import后把G标为`Imported`，该状态仍位于既有一字节padding内，并在目标P真正dequeue前禁止再次转移，因此无需恢复per-spawn `*G` hint也不会在两个空P间回弹。claim、mailbox publication、executor request、route close strong join和owner recheck都使用固定POD状态；争用或mailbox满时保留本地FIFO，不等待、不自旋。

Channel completion另有一条严格受限的因果return-route路径。正在managed resume中的Go任务执行typed hchan物理提交时，runtime从当前scheduler G的exact executor直接取得producer route；独立native线程、foreign-thread callback、timer、I/O和其他异步producer一律写route 0，同一logical G的同步C-to-Go reentry仍属于原managed resume并可继承其route。该advisory hint只占用`channelOperationSlot.physical`原子字的高9 bit，在旧owner完成select仲裁、detach和result Take后复制到`ParkState.seed`的materialized phase overlay，因此不扩大channel slot、G、`ResumePacket`或compiler frame ABI。源P只检查ready head对应的一个exact preferred route；目标必须已经发布`Requested` demand，不能扫描其他空P，也不能把peer捕获到仍在计算的producer之后。durable mailbox publication成功后，runner通过单一core commit清除“最后一个local ready已被转移”产生的旧`readyDebt`；若源队列仍有可运行G则保留该debt。该路径目前只覆盖能在旧owner完整物化的Channel结果；其他source继续产生route 0，未证明P-neutral的等待仍禁止迁移。

这已经证明有界8-route fleet内对P-neutral initial、yielded和已物化park continuation的通用按需注入/工作共享。1/4/8 route core生命周期通过race测试，固定topology下`GOMAXPROCS=1`的channel park/progress、`GOMAXPROCS=8`的物理peer启动/分发/停止/全join以及运行期1→4→1并发上限变化均通过真实linked E2E；普通同步Go源码也已直接调用标准`runtime.GOMAXPROCS`与`runtime.SetDefaultGOMAXPROCS`入口。同M阻塞的第一阶段又在C调用前后成对释放/重获managed-execution permit，并以`GOMAXPROCS=1`对抗E2E证明另一route可继续运行。完整handoff的公共baton现已独立为8-byte无指针`ExecutionDomainHandoff`：generation与owner epoch同一身份，claim与return原子竞争，已claim owner必须在稳定边界finish，complete先清epoch再允许reuse，耗尽永久retire。gate归属M/execution-domain slot而不是P，因此replacement再次阻塞时可用自己的gate形成受线程上限约束的链；它不包含线程创建、等待或第二套scheduler。M-local `ExecutorResumeHandoff`在exact issued-resume窗口detach P/current/action/budget/lock owner，把active G置为不入queue的`GForeignWaiting`，并只在driver无issued action、source transaction回到Idle的边界restore；registered cancellation在等待期间保持sticky，G/P固定布局不增长。

Native replacement物理协议也已接通。进程保留10,000个logical M slot，但静态目录只内嵌初始8个owner和157个page root；其余slot首次使用时先以1-byte marker CAS取得唯一发布权，再分配并发布64-owner稳定页，因此reader不分配、并发败者在`nogc`下也不泄漏候选页，静态目录对象已从1,120,040 bytes降到2,192 bytes。每个route另有一个原子active-M槽，C pthread入口仍只携带`uint32 slot`，且不观察任何Go page pointer。锁线程foreign边界按`detach resume -> begin baton -> reserve replacement slot -> release managed permit -> create replacement thread -> original M执行C -> request return/ring -> join -> reacquire permit -> restore exact resume -> recycle slot`执行。permit必须在线程创建前释放：quota holder按route而非M计账，允许child抢跑会被正确判为同route double-acquire，而不是普通quota contention。replacement只借用原P、driver、source catalog和公共bounded reducer；它不结束原owner epoch、不建立第二套scheduler，也不能因为被detach的command G不在P队列中而误关command executor。逻辑driver保持Active时，`borrowedWait`只作为物理wait publication；durable source/request仍是事实源，后到producer通过原route doorbell唤醒replacement。replacement reactor复用同一timer deadline和poll operation snapshot，nested locked foreign再次使用replacement自己的handoff gate，因此M0→M1→M2链不绕过generation/epoch或quota规则。真实linked E2E已覆盖同route channel/select、timer、socketpair poll/read、最后可见子G完成以及nested replacement，并验证原locked G返回原物理线程。

同一replacement协议现在也闭环了active same-M C→Go重入的非返回结果。编译器只给`managed-callback`/`managed-ingress`调用选择V2入口并生成exact typed adapter；`reentry=none`仍走不含setjmp和status fanout的V1。V2在原M栈上持有唯一`sigsetjmp` landing，adapter把普通或recovered return重建成C返回；Panic、Goexit、Abort、Shutdown先保存type/data/status，再`siglongjmp`越过当前C栈。landing端必须先strong-join replacement、恢复TLS与exact LLVM resume，之后才复用现有parent cleanup dispatcher。没有active V2 landing的独立host export以及误标no-reentry边界继续fail closed。真实linked E2E已证明panic可由外层Go defer recover、Goexit会跳过C调用后的代码、两种退出后boundary可再次正常使用；runtime测试覆盖完整status集合和嵌套record。尚未完成的是无active episode的runtime-owned/foreign-created-thread ingress、retained callback lifetime及非native target的等价non-return策略。

未配对`UnlockOSThread`的G退出现在通过`Action`既有padding中的target-neutral retire bit传递；normal、bounded destroy receipt、panic terminal和command cancel都不会丢失该义务。Native owner先取得physical-stop succession lease，再由启动期clean factory创建并确认一个clean M已接管同一route，旧pthread随后detach/exit；program M、普通peer M和temporary replacement lineage均使用同一目录协议。replacement lineage root只保存scalar current-slot，blocked parent等待baton Returned后join exact current successor，不能从函数地址或旧pthread反查。真实linked E2E用pthread TLS destructor证明三种旧M确实退出。若Go main已返回而后台locked M仍在不可取消C调用中，native按Go进程语义直接`exit(0)`，不会为“优雅join”无限等待；可收敛command shutdown仍执行原有cancel、全peer join和factory stop。fatal路径继续process-fail-stop，不尝试复用受污染M。

每次locked blocking call现在先尝试复用有界8项standby-M缓存；缓存为空才由clean factory创建pthread。temporary lineage返回时，parent仍持有exact Returned baton，C wrapper以进程内单调且永不复用的physical token加scalar M slot确认Go入口已经返回；只有该确认完成后logical M目录slot才回收，raw pthread才可被下一slot重新分配。C factory不再预留10,000项record与slot map：它只为实际pthread分配48-byte C record，以mutex-owned intrusive list完成生命周期查找，join/self-retire后释放；静态factory从440,160 bytes降到176 bytes。缓存满时立即strong join，target close按peer、worker、standby、factory顺序强关闭。10,000仍只是logical slot/并发深度及物理线程账本的fail-closed默认上限，不是预创建线程数或静态record数。独立物理线程账本精确覆盖program、clean factory、4 worker、7 peer、successor/replacement及standby pthread；创建前reserve，join或永久pthread exit后release，`runtime/debug.SetMaxThreads`按Go的MaxInt32 clamp、返回旧值和低于当前M数即fatal语义直接操作该账本。C并发/restart/8项溢出、旧token拒绝、14线程上限下连续replacement复用、program-successor terminal close以及普通标准库`runtime/debug.SetMaxThreads`同步调用均已通过。Command root由G尾部padding中的一字节`AnyOwner/CurrentOwner` mobility保持program P，首次入队建立隐式owner，唯一跨P mailbox只接受AnyOwner，因此没有每G P指针或affinity registry；peer→program反向spawn验证main return仍由正确边界发布。owner-assisted bounded half-batch现已替代逐G demand transfer；它仍不是无界global run queue、并发local-deque随机steal或完整thread/realm affinity策略。local deque仅是profiling驱动优化；完整标准库/GOROOT的LockOSThread、syscall、callback/reentry及非command host shutdown矩阵仍需验收。后续扩展继续复用同一个需求/transfer与handoff协议，不能增加spawn、source或native-only所有权旁路。

2026-07-26的第一阶段物化接受零或一个Timer、Manual、Poll、Worker source。`WaitSetRecord`可绑定compiler/runtime提供的52-byte、align-4、pointer-free frame-local `ResumePacket`；原owner在`parkReady` promotion事务中先Consume winner lease，再按source类型Take/Discard payload、确认quiescence并回收exact generation，最后把`ParkState`压缩为不含winner/source ownership的`parkMaterialized`。Worker标量和Poll枚举在这里复制，Timer/Manual不保留payload；迁移后的resume只消费packet，prompt task cancellation也只抑制packet，不回访旧source。packet-bound wait不能走缺少`ExecutorSourceSet`的legacy promotion入口，旧`parkReady/parkConsumed/parkDelivered`仍被迁移gate拒绝。

第二阶段已把direct Channel和multi-case channel `select`接到同一机制。compiler-spilled `ResumeCleanupPlan`只保存固定`ResumeCleanupKind`、typed runtime context、`{entries,count,stride,idOffset}` ID range、source/claim和阶段游标；没有callback、interface、函数地址反查或永久G字段。common resolver在旧P上Consume exact decision后停在`ExecutorRunStepMaterialize`，runtime direct switch每次处理一个logical case：从hchan队列摘除typed waiter、完成必要的buffer/close reconciliation、复制winner的封闭`waitStatus`，并清除frame中的hchan/source/claim指针，只暂留exact ID。随后core逐ID执行`ConfirmQuiesced`，统一reset共享claim、Take/Discard winner lease，再逐ID Recycle并清零；最后才写入P-neutral packet、转成`parkMaterialized`并进入ready queue。direct、两case winner/loser、close、task cancel、native race/shuffle、JS/WASM、旧P source为空后跨P迁移，以及迁移后prompt task cancel均已覆盖。真正发生的Yield/Park会确认已经由本次suspension满足的G-local preempt request；executor/source request仍有自己的sticky gate，因此不会因残留`preemptRequested`误拒合法迁移，也不会丢服务义务。

第三阶段把typed materialization内部的hchan reconciliation进一步拆成frame-local runtime子游标。每个`ExecutorRunStepMaterialize`现在至多摘除当前case waiter或处理一个peer waiter；buffer receiver、buffer refill、closed receiver和closed sender各有显式phase，phase-only前进也单独占一个reduction。hchan mutex不会跨runner返回保留，select resume也不再线性清零case；core recycle清掉最后的ID后，resume只消费packet并清共享state。native与JS/WASM测试以64个discard/commit peer验证任一步queue变化不超过一个节点。该证明只界定materialization的peer数量；单个channel element的固定上限copy成本、park preparation/sort、普通try路径的discard扫描以及同步`close`全队列drain仍需各自的cost gate，不能扩大表述为完整hchan已全路径常数成本。

第四阶段已把单Worker HostOp和Worker+Timer deadline接到同一个typed plan。`RuntimeCount`与物理source `Count`正交：HostOp只产生一个固定枚举runtime materialization step，在旧owner上退休host adapter及control transport；随后common core分别确认Worker/Timer quiescence、取得winner payload、回收exact generation并清零frame ID。Timer先胜而Worker仍在执行时，resolver保留`AwaitExternalFact`，直到物理取消确认到达才恢复promotion，不制造无事件忙转。Worker标量直接写入已有52-byte `ResumePacket`，96-byte native/72-byte WASM32 plan不重复保存payload；恢复后不再调用worker driver、run decision或旧owner transport。实际WASM host-owned file与带deadline network探针均从Go同步调用风格完成。

第五阶段已把semaphore/notify keyed park的runtime-private registry接到同一plan。frame先绑定完整cleanup descriptor，再把POD key/generation发布到registry；旧owner的唯一runtime step退休exact registry generation，common core随后确认、消费或丢弃并回收route-local Manual source，resume只取packet。`Posting`竞态不再循环或新增scheduler stop：producer在Manual source Post前先把private slot发布为`Delivered`；若取消先退休Posting generation，producer观察retired并跳过Post；若Delivered后Post并发，Manual source自身的producer admission、close和quiescence决定Post成功或stale，frame和G都不会暴露给producer。标准库opaque state冻结为pointer-aligned 256 bytes，实际runtime布局为native64 256 bytes、WASM32 196 bytes。native/WASM race/layout、generic Manual P-neutral core以及真实同步风格`Mutex + Cond + WaitGroup`程序均通过。600路真实WaitGroup验收随后暴露旧实现会在可抢占`claimOne`扫描中持有required-plain自旋gate；registry现已改为每槽一个32-bit generation/state CAS控制字和原子immutable snapshot，注册、claim、finish与retire均可安全抢占且不新增`//llgo:coro`标注。固定2048槽的`claimOne`仍作有界线性选择，后续高争用优化不能被表述为已完成O(1)。

两字`OperationID`已经用稳定`RouteID`解决多P source namespace：两个P的同类source可以都从local slot 1开始，但callback必须携带完整`{source, route, local, generation}`，通过route admission精确发布source fact并请求对应executor。P teardown先seal route并strong-join producer，旧route留下永久tombstone；该路由约束不允许重新引入Go pointer callback ABI或按fd/函数地址反查owner。

推荐的V2编码保持两字布局：`word0 = source:8 | route:9 | local:15`，`word1 = operationGeneration:32`。`route`是runtime instance生命周期内单调分配且不复用的`RouteID`，route close后留下永久tombstone；local slot和operation generation都从1开始，generation不回绕。这样只凭POD ID即可O(1)找到owner source，不引入per-operation全局目录，并保留完整32-bit热slot generation。超过511个lifetime route或每route/source超过32767个live slot的profile必须选择versioned wider/flat-directory ABI并明确内存代价，不能偷占generation bits。

Native operation catalog已从每个P预留最大Timer/Poll/Manual/Worker/Channel数组改为按需稳定分页。每类source保留一个allocation-free 64-slot inline page和8个两级目录根指针；跨过每64个dynamic page才增加一个64-pointer目录块。owner只在不可逆ParkSet之前分配并release-publish完整page路径，producer仍只携带两字`OperationID`，已发布slot地址永不移动；WASM、embedded和baremetal可以停留在inline page或提供自己的稳定arena。Native逻辑上限仍为Timer 4096、Poll 1024、Manual 2048、Worker 1024、Channel 1024。相同标准Go workload的zero-fill从11,380,814降至2,070,270 bytes（-81.81%），而文件仅增加688 bytes、`__text`减少20 bytes；600路timer和WaitGroup以及file/TCP纵向验收均通过。独立M目录随后采用更简单的一层64-owner page，因为它只有owner lookup而没有source ready index；标准`sync`验收的zero-fill又从2,076,278降到958,430 bytes，文件减少442,944 bytes。两类目录共享“stable address + release publication + reader不分配”的原则，但保持不同布局和生命周期，不强行抽象成一套过重机制。

第一阶段外部task handle可以把G视为pinned；开放其迁移前，control endpoint必须拥有原子current-route locator。到达旧route的sticky fact转发到新route并再次校验，迁移竞态最多再次转发，不能丢失或在旧P执行cleanup。普通没有外部handle的G仍不增加全局registry或常驻route pointer。

### 6.2 各目标映射

| 目标 | 初始映射 | Event wait | 并行扩展 |
| --- | --- | --- | --- |
| Native POSIX | N个M驱动N个P，初期允许1P配置 | poll/epoll/kqueue或completion backend + doorbell | local runq、global inject、work stealing、blocking worker补偿 |
| JS/WASM | 1个host M/1个P，按slice返回host | Promise/timer/requestRun | 通常只有并发无并行；Wasm threads profile另行增加P |
| WASI | 初期1M/1P | preview pollables/poll_oneoff | host支持threads时增加P |
| RTOS | 1..N executor task/P | notification/event queue + one-shot alarm | P可固定到RTOS task/core |
| Baremetal | 1 core/1P main loop | IRQ event ring + hardware alarm + WFI/WFE | SMP target按core建立P，跨coreIPI doorbell |
| Embedded host | host显式调用RunSlice/Poll | host注册callback/alarm | 由embedding contract声明是否允许并行re-entry |

并发语义不能依赖平台有多个线程；并行只是P/M数量和source routing的配置。

## 7. 抢占

抢占属于scheduler core，不属于timer source。

- Compiler在所有可能无界的 managed path插入suspendable poll；runtime还必须给立即ready wrapper、连续child await、source drain和ready task连跑扣除同一service budget。
- Runtime维护独立 `preemptRequested` generation/bitset。
- Native sysmon/tick、WASM slice budget、RTOS tick和baremetal IRQ只负责提出请求与唤醒executor。
- Timer deadline只是一个event deadline；即使没有active timer，CPU-heavy G也必须有界让出。
- `nopreempt`区域必须短、可验证，并在退出时立即处理pending request。
- Post-optimization verifier或等价证明必须保证循环backedge和超长path仍有poll。

## 8. 文件、网络和 timer 如何映射

### 8.1 Timer

当前公共timer core使用scheduler-owned、绑定期间容量冻结的固定分页`TimerRegistrationTable`：每页64个slot，按target profile在bind前附加稳定page，并以有界线性scan发现deadline。slot只保留当前typed operation、generation、operation record和result lease生命周期；旧Timer模式及跨模式generation兼容分支已经删除。dynamic/sharded heap是语义稳定后的容量/性能升级，不是当前已落地事实。平台仍只实现monotonic clock、arm earliest deadline和executor wake；`Sleep`、Timer、Ticker、AfterFunc和deadline应复用这一source契约。

### 8.2 网络和可poll fd

`internal/poll`向netpoll source提交fd、interest、deadline和result record；当前G park。Readiness到达后由owner按Go wrapper契约执行一次或重试operation。Raw syscall本身不能擅自改变EINTR、short result或EAGAIN语义。

host-owned pull target不伪造POSIX readiness fd，而把一次完整I/O作为同一个
`HostOp -> WorkerOperationSource -> ParkSet`事务提交。当前class-2 verb只扩展薄平台表：
`read/write/accept/connect/recvfrom/sendto/recvmsg/sendmsg`共用两字exact
`OperationID`、read/write control lane、deadline timer loser barrier和Close物理取消确认；
compiler、executor和source状态机没有按verb分支。`recvmsg`用固定36-byte POD回传
target-neutral sockaddr与message flags，并以第二个scalar result回传OOB长度；连同payload、
OOB和地址的借用指针共8个operation word，未突破统一9-word transport。该结构只覆盖当前
IPv4/IPv6 host ABI；Unix domain、raw packet及真实ancillary control-message语义仍需各target
扩展和验收。

compiler只在ProgramIR freeze读取一次HostOp的raw SSA形状，并冻结
`{opcode,pointerMask,argumentCount,metadataWords}`；physical plan与emitter只能消费该POD
recipe。HostOp和bounded worker共享同一个word-call Park模板，不能各自再建一套suspend CFG，
也不能在codegen按函数地址、参数类型或opcode反推transport策略。

control lane同时保存exact `OperationID`与单调非零epoch。`SetDeadline`/`Close`先发布
descriptor状态，再推进epoch并取消当前已绑定operation；park hook在提交后重读epoch，
若与compiler传入的snapshot不一致，就把`ParkCancelOperation`作为preparing/sealed
ParkSet的sticky事实交给首次source visit，随后走普通exact物理取消、terminal ack和result
discard。这样配置修改落在snapshot与bind之间时也不会丢失。host pull transport还显式保留
“Submit尚未交付且Cancel已请求”状态：同一个generation必须先向embedding交付Submit，
后续pull才可交付Cancel；embedding不需要也不允许为从未见过的operation缓存取消。

### 8.3 普通文件和不可poll operation

POSIX regular file、DNS或阻塞C调用根据target capability选择：

- io_uring/IOCP等completion backend；
- 有界blocking worker pool，operation record在worker期间保根/pin；排队任务允许cancel-before-start，已启动任务只有best-effort physical cancel并仍需接收late terminal fact；
- thread-affine专用M；
- 单线程host的async import；
- 不支持目标上的明确capability诊断。

worker queue满必须确定地失败或背压，shutdown在owner P之外join已启动worker并等待source quiescence。不允许为每个operation创建一个G专属pthread、无限增生补偿线程或保留调用者native stack。

`//llgo:coro worker`只可标在一个精确的bodyless C声明上，并冻结为
`link identity + physical symbol + structural ABI`的域隔离证书。它承诺一次调用可在任意worker线程执行、在worker发布完成前同步返回、不回调或重入managed Go、参数按值传递，且返回前不保留任何Go pointer；它不承诺短延迟或nonblocking。函数地址先进入`uintptr` carrier的路径使用正交的`workeraddr <arity>`前向事实，consumer不得再从地址反查symbol或策略。两类指令都与`noblock`、`sync`互斥；声明级`schedulerwait`已经删除，raw-host直调只由编译器对exact host root及其closed call closure逐调用推导。SSA plan保留普通`ExternalUnknownForeign + BlockForeign + WaitForeign`语义，不能因raw-host授权或worker证书而弱化异步染色。worker lowering必须先验证closed call、精确producer及目标宽度下可无损word-pack的参数/结果，再要求plan和frozen emission universe两侧证书完全一致；任一侧缺失、identity不等、ABI不支持或普通未标注foreign declaration都fail closed。当前审计目录已覆盖本轮文件/TCP链所需的固定Darwin file/socket/address-resolver/sendfile等leaf以及少量runtime leaf，不再只有`C.write`；fork/exec、线程控制、未知ioctl/kevent/ptrace和未审核目标仍保持raw/plain隔离或拒绝。像`gai_strerror`返回进程期C指针的特殊结果由独立foreign-pointer result contract声明，不扩大普通worker scalar ABI。

Linux共享`RawSyscall` carrier还要求与函数地址证书正交的trap证书。精确I/O常量和只读credential query（`CAPGET`、`GETUID/GID/EUID/EGID`、`GETRESUID/GID`、`GETGROUPS`）可使用worker；当前支持域内credential mutation仍fail closed，固定worker fleet继承同一启动credential。未来启用Go的all-thread credential mutation前，必须把同一事务扩展到全部fleet worker，不能提前外推兼容性。动态trap、credential mutation、thread-affine和process-control仍fail closed。标准库vfork/clone child专用的`forkAndExecInChild1`与`doCheckClonePidfd`由source annotation取得`RawCritical`双实体，managed caller只在该精确call occurrence选择原生栈plain variant；不能把`EXIT/EXIT_GROUP`整体误声明为worker-safe，也不为标准库函数名增加lowering白名单。

## 9. 当前实现审查

### 9.1 2026-07-25 当前垂直验收

本节记录当前工作分支的垂直切片，不覆盖下文保留的 Phase 22–32 历史审查。验收硬边界是：

- 用户程序和 Go 标准库必须保持普通同步 Go 源码风格；只允许使用 `time.Sleep`、`os.File.Read/Write`、`net.TCPConn.Read/Write`、`go`、channel 等标准语法和 API。
- 验收 fixture 不得 import LLGo/LLVM 实现包，不得出现私有 `Future`、`Await`、`Task` 或显式 callback 改写。底层 wrapper 变成 `MayPark` 后，由 effect 固定点自动染色所有 managed caller。
- 只有“使用 LLGo 编译普通标准库源码，并且编译、链接、运行与可观察语义全部通过”才算 E2E 通过。host Go 运行成功、source-selection 测试、runtime 单元测试或手写 runtime driver E2E 都不能替代这个门槛。
- OS/host I/O、timer、同步原语及其他外部等待不得占住executor；必须先stack-cut并park，或转交bounded worker/平台event source。每一种接入都要有等待尚未完成时另一G仍能继续运行的进度测试。当前允许纯计算暂时占用一个P；生产级并行在P-neutral result/runnable之后以M/P/G、动态P/steal和blocking compensation解耦。blocking compensation必须复用`ExecutionDomainHandoff`的generation/epoch baton和`ExecutorResumeHandoff`的active-resume detach/restore gate；target只补充replacement owner容量、active M slot、doorbell与create/join，不能再定义平行的claim/return或scheduler生命周期。

核心能力已有以下可运行/可编译证据，用来确认compiler、coroutine frame、executor和source契约能在真实链接边界上闭环：

- native no-stdlib runtime island已链接运行channel、static spawn与closed-channel/send-closed fault路径；
- explicit panic已验证child payload和parent propagation的production链接执行；
- native timer已与CPU hot loop同时运行，由compiler safepoint、有界`RunSlice`和timer due drain完成公平唤醒，hot loop不需要显式yield；
- `time.Sleep`已改为compiler-owned Timer V2 typed park recipe；定向lowering/CoroSplit、current-owner、cancel/shutdown与race验证已通过，不再依赖按timer符号猜测frame retention；
- 受控Timer manager的wait/Stop/Reset已迁移到compiler-owned Timer V2 typed recipe和exact `(controller, generation)` park/cancel/resume；旧controlled V1 prepare/park/cancel/retire ABI、build root和symbol-specific frame-retention特判已删除，exact cancel、prepare-publication race、winner lease和shutdown-before-deadline定向验证已通过。这些只证明transport/ownership core，不代表完整Go Timer语义已通过；
- `internal/poll.runtime_pollWait`已改为compiler-owned Poll V2 typed park recipe；定向lowering、current-owner retained reactor、deadline/closing/cancel/shutdown与race验证已通过。外部Node embedding现已在`wasm-unknown`与`wasip2`真实运行标准库同步TCP/UDP fixture，覆盖HostOp提交、alarm、动态deadline、Close取消和物理ack；这仍不等于普通command自带reactor；
- production `TaskControl`端点已通过register/post/close、exact executor request、safepoint claim与terminal竞态测试；
- allocation-free `ExecutorFleet`核心、route registry和P-neutral runnable transfer已通过core/race测试并接入native程序入口；route 1由command线程执行，其余7个固定route由独立pthread M并行执行，所有domain共用同一物理reducer并各自拥有P/source catalog/doorbell/reactor。普通空domain通过同一`IdleArmed -> final scan -> CommitSleep`事务进入standby，由routed request精确唤醒；完整8-route启动、shutdown publication、route strong-join、全部peer join和driver/source回收已形成完整target lifecycle。逻辑execution quota在真正的managed resume前获取、reducer commit后释放；无permit的route仍服务事件，且运行期缩容不撤销in-flight resume。所有已支持Park source在旧owner物化为P-neutral packet后才允许迁移；
- locked-M blocking compensation已从“仅释放execution quota”升级为完整同P物理owner替换：active resume私有detach，generation/owner-epoch baton转移，稳定scalar M slot创建/claim/return/join，原M重获permit后精确restore。replacement继续驱动原route的ready、channel、timer和poll source；同route channel、30ms timer、真实socket readiness及M0→M1→M2嵌套阻塞linked E2E均通过。普通replacement子G的final-root receipt只结束该G并保持program driver Active，不会把私有rooted的detached command G误判为进程终止；
- native poll descriptor已从共享Go map/fd反查改为每FD一个C分配的opaque scalar owner；read/write方向各原子保存deadline和完整`OperationID`，park/close/deadline update通过seq_cst handshake精确投递route。owner同一轮同时观察到到期deadline与已排队readiness时由deadline胜出；deadline reset/remove通过frame-retained snapshot与当前descriptor重检后重新注册。双owner TCP probe已fresh compile-link-run，并完成8路共10,000次压力而无deadline/readiness误序；
- managed heap allocation已在native和wasm32通过compile/CoroSplit、spill/reload、exact-root profile与zero-size sentinel测试；
- coroutine string concatenation已收敛为对`runtime.StringCat`的精确structured-outcome lowering，overflow panic沿显式结果传播，native/wasm32 compile、CoroSplit与object emission已通过；
- slice到array pointer/value转换的`N>0`长度失败已进入同一explicit-status fault/recover路径，`N==0`保持nil与empty-non-nil slice的原始data pointer语义。operand-free fault继续使用allocation-free V1静态payload；需要运行时操作数的转换失败使用正交V2 payload/prepare ABI，把目标array长度和源slice长度作为两个target-width word直接从lowering传入，并在异常路径分配GC可见的`boundsError`。其动态类型、`runtime.Error`分类和Go 1.26逐值错误文本均已由runtime contract以及GOROOT `convert4.go`的真实compile-link-run验证。V2的两个hook、精确签名和ABI identity已经进入required-root、bootstrap digest与physical ABI hash；未知kind、operand不匹配或缺失V2 runtime一律fail closed，不退回丢失参数的通用文本。
- 泛型intrinsic已能从精确instance解析origin和SSA body，`llgo.index`在native/wasm32均按inline-no-suspend语义lower，不伪造可调用函数体；
- Darwin已补齐`internal/cpu`两个sysctl bodyless声明的精确runtime bridge和C sync certificate；另外，一参数、不重定向的visibility-only `go:linkname`已冻结为独立证书，paired bodyless consumer只选择managed `$coro`入口，不生成raw plain body。

这些是core-first证据，不是标准库兼容性证明。native compiler command entry仍是固定机器栈V2循环，但target start现已把program P/driver/source/registry作为route 1原位收养，同时创建固定7个pthread owner；program return会停止并join全部peer与共享worker pool，再按route、backend、driver顺序强关闭。Timer catalog、Poll V2 callback与Worker transport均使用exact route：Timer由各domain owner的单调时钟扫描；Poll和Worker的两字`OperationID`在route admission内完成source publication、exact executor request、duplicate/stale分类和关闭强汇合；Worker复用一个进程级固定物理池。零/单source、Channel/select、HostOp deadline和keyed/private-registry park的typed materialization、demand-driven global injection/work sharing、owner-assisted bounded half-batch、动态`runtime.GOMAXPROCS`、locked-M同P replacement、普通locked Yield/Park与compiler-poll preempt、三类clean owner succession、有界standby M和真实`runtime/debug.SetMaxThreads`均已接通；command main-return也不会等待不可取消的blocked M。尚未完成的是thread-affinity跨target/回调完整策略以及callback/reentry与非command shutdown矩阵；并发local deque仅保留为profiling驱动优化。target-neutral host-owned pull adapter已完成core/race与JS/WASM、WASIp1、baremetal、显式embedded交叉编译验证：`more`只发布later-turn action，idle只暴露POD executor/generation/epoch/deadline，alarm/notification取消后才可复用epoch，shutdown seal两个ingress并strong-join callback tail。compiler的host-target V2 entry也已完成：它只执行一次有界initial slice，保留`next_action/profile/next_deadline/publish_time/ack_cancel/continue/post_wait`等callback reference roots，仅接受canonical `Complete`或executor slot/generation/epoch/flags/deadline均精确的`Yielded`/`Suspended` tuple，后两者detached返回host，不在entry内继续调度或递归re-entry。仓库Node runner现已作为显式embedding真实消费这些action，并在`wasm-unknown`/`wasip2`运行file与TCP/UDP标准库fixture；命名`wasm` command也已内建同一pull pump并真实运行墙钟、时区和`time.Sleep`，Schedule/Alarm分别用microtask/timeout且没有Asyncify。该JS command目前对非空HostOp明确拒绝，不能把timer证据外推为JS文件/网络或FuncOf/reentry兼容。普通WASI Preview 1 command现也内建同一host-pull协议的单线程reactor：在managed slice返回后用一个`poll_oneoff`集合等待absolute alarm与fd read/write，并支持`syscall.Open/Read/Write/Seek/Close/Unlink`；并发timer+文件fixture已由Wasmtime运行。它不支持Preview 1网络，高层`os.File`仍被通用interface exact-effect候选缺口阻挡；`path_open`/unlink/metadata又没有Preview 1标准pollable形式，当前同步host import属于准确报告的Degraded compensation，慢host filesystem仍可能暂停整个single-thread command。Preview 2 component/pollable lifecycle也尚未接线。JS HostOp/HostReentry、WASI Full nonpoll compensation/Preview 2、RTOS HAL与baremetal IRQ/WFI glue、完整cleanup/defer/recover/Goexit lowering以及precise/moving GC仍是明确缺口。

执行器已收口到一套物理run-step reducer；fleet外层只保留exact domain owner、显式单调时钟和budget，不存在第二套resume/destroy/commit语义。空fleet P使用公共timer-aware idle事务完成standby admission；提交后先释放owner epoch，route-local固定poll set执行fault-containment有界物理pass，doorbell/fd/deadline唤醒后再取得新epoch并执行公共`WakeExecutorAt`，只完成idle→active并设置`sourceMore`，所有source解析和typed materialization继续由同一run-step reducer完成。idle prepare/commit中若request或事实源赢得竞争，也执行相同的“leave idle + sourceMore”转换，不再嵌套调用legacy全量poll。普通domain最后一个G销毁只完成该G并保持executor可接收未来routed work；command route的main-return语义仍由program coordinator独占。实际peer M循环、共享worker pool lifecycle、分布式shutdown和强关闭引用清理现均已接入并通过production-island、source/race及TCP E2E验证。

Worker transport的route-aware核心现已完成：仍只有一个进程级固定4线程/1024-job物理池，C11 sequence ring允许多个exact P owner并发预留不同cell；每个reservation在compiler-enforced no-suspend hook内submit或以tombstone cancel。Job不增加Go pointer或全局operation目录，直接使用既有两字`OperationID.SourceSlot`中的route；完成端按compiler-reserved target profile静态选择legacy program或fleet ingress，fleet再完成exact source/request/doorbell投递。owner在把完成发布为可resume winner前seal generation并等待所有已admit producer退出，避免回调release尾与result recycle竞态。C11环已通过route 1/2并发producer、交错wrap、四consumer和stop/join测试；program-level fleet coordinator实际start/stop该池，当前fleet标准库profile不再退回single-P worker transport。

Worker transport现又把compiler-known source C entry作为独立scalar `traceTarget`随job传递，并把completion扩展为互斥的普通三字结果或`{fault kind, ucontext PC, traceTarget}`故障结果。callback-free worker调用中的`SIGSEGV/SIGBUS/SIGFPE`只在C TLS中执行bounded capture和`siglongjmp`；owner G恢复后验证结果、从当前LLVM frame descriptor取得活动Go帧，再与compiler shadow callers组成panic snapshot并进入统一ExplicitStatus fault lowering。连续三次recover、recover后的`debug.Stack`、未recover终端traceback以及bootstrap传播`Panic/Abort/Shutdown/Goexit`均已由Darwin/arm64真实LLGo compile-link-run验证；正式`TestCallerAcceptanceCFaultRecover`、`TestCFaultRecoverable`、`TestCFaultTraceback`全绿。managed C-to-Go reentry现在进入Go前detach当前C TLS landing scope，普通返回C时rearm；callback的非返回退出不会恢复已失效的C activation。精确ucontext PC当前只覆盖Darwin/Linux arm64与x86-64；其他native架构遇到无法取PC的hardware fault会fail closed，不能把本地证据外推为全平台C fault兼容。

2026-10-13运行验收补齐了两条独立的编译器规则：

- C函数值的managed adapter必须保留在动态调用目标集。静态code-address过滤与publication inventory共用同一份adapter投影，不能因原始C声明被投影成Go wrapper而丢失目标并误选空的plain entry。
- LLVM executor-leaf证明只授权不阻塞的直接调用，不授权省略故障恢复。证明同时产生独立的`MayFault`事实，绑定完整LLVM call closure、target、ABI和证书identity，并保存为调用证书的`NativeDefaultFaultBoundary`。本地声明与归档导入声明均从最终SSAPlan的实际调用边产生native fault capability seed，再复用已有函数调用图传播和native resume landing。当前 archive v16保留该事实，并携带编译时谓词的消费约束（包括 false）；不兼容的可达归档入口必须重新编译，不能在 consumer 中对无函数体的声明重新染色来修补。地址观察、死分支和已消除调用不产生需求。无需新增源码标注、worker路径或第二套异常模型。

可选 GC 完成通知复用 execution-domain 的冻结谓词通路，而非新增调度层。
`GCObservation` 仅由可达的 runtime `EnableGCObserver` 注册入口及其归档事实产生；
弱引用自身、调度器查询、allocator 初始化均不反向开启它。
`llgo.coroGCObservationEnabled` 在 effect/ABI 分析前绑定，普通 Go 调用链自动传播。
无 channel timer 时，allocator 的 collector hook、scheduler 的观察调用、tinygc 完成通知、
native fd detach，以及标准 runtime 的原生导出 wrapper 都在同一冻结 CFG 中裁剪；
尤其不能让无标准库导入的程序仍引用 `__llgo_coro_gc_observe_v1`。
两个谓词各自记录消费 mask 和 true/false 值，archive v16 和 plan digest v41 均绑定它们。
这不增加 G、frame 或 timer 的运行时字段，也不引入 callback registry 或普通 Go 函数标注。

`MayFault`对未知地址读写、atomic、整数除余和其他已接受intrinsic保守处理；确切的普通/TLS global槽、类型匹配且非空的直接alloca槽、纯算术叶子不产生该需求。current-G TLS读取在`-O0/-O2/-Oz`下均有无额外landing的门禁。此事实不声称所有C未定义行为或硬件trap都能转成Go panic，仍受平台信号和`SetPanicOnFault`策略约束。

`TestCallerAcceptanceForeignReentryFaultRecover`已在Darwin/arm64、Go 1.27、LLVM 22下fresh compile-link-run通过：回调先经timer挂起，再分别覆盖返回C后的故障、回调内部动态调用C叶子故障、callback panic逃逸和后续正常调用，连续三轮恢复。该证据只覆盖此纵向路径；完整`test/`、`_demo`和GOROOT验收独立统计，不由单个回归用例推定完成。本阶段按运行验收优先，暂不以`cl/_test*/**`指令golden差异作为阻塞项。

同轮运行验收还固定了故障边界的所有权：物理resume的landing链保存在M，G的`defer_`仅保存逻辑defer链。每个landing沿用已有`Defer.Args`保存进入时的逻辑cursor；panic先执行该cursor以上的普通defer，再进入物理landing。`GetThreadDefer`、`SetThreadDefer`和`ClearThreadDefer`不读取或覆盖M的landing链，正常pop也不改写G的逻辑cursor。这只增加每M一个指针，不增加每G或每coroutine frame的字段。启动时用于TLS的物理G不领取用户goroutine编号，首个逻辑main G仍为1。

2026-10-13的v7本地验收（Darwin/arm64、Go 1.27、LLVM 22）中，`test/llgoext`的23项测试全部通过，包含defer-head隔离和130个G的ID分配；C故障恢复、managed C-to-Go重入、连续恢复和未恢复traceback四项编译运行测试全部通过。此前同轮的`test`、`test/cgo`、`test/syncpool`分别通过41、3、1项，`test/std/runtime`通过5项。上述结果不是完整`test/go`、标准库或跨平台验收的替代。

#### 少量标准库能力探针政策

核心契约完成后，只保留少量、固定且使用普通Go源码的标准库程序作为能力探针。最小纵向门使用`time.Sleep`验证timer/park/deadline，使用标准库固定导出的`syscall.Open/Write/Seek/Read/Close/Unlink`封装完成一字节文件回环，验证bounded worker/syscall/result ownership；完整`time.Timer`验证controlled Timer V2上的Go timer基本语义，高层`os.File`验证interface/defer与regular-file worker封装，loopback TCP验证readiness/deadline/close-cancel链路。2026-07-23又加入`syscall.Pipe`进度门：main G先执行必然等待的`syscall.Read`，另一G只有经过timer后才`syscall.Write`；若Read留在executor上，程序必然死锁。该用例已fresh compile-link-run通过，连同原五项构成六个探针；TCP另有8路共10,000次压力证据。首个失败点必须归纳为缺失的通用compiler、runtime core、source或target adapter契约并在该层修复；不得添加按库名、函数名或fixture匹配的特例opcode/lowering，也不得绕过统一executor/event模型。

`LLGO_CORO_STDLIB_ACCEPTANCE`的`time`、`timer`、`syscall-file`、`syscall-pipe`、`file`和`tcp`程序已冻结上述源码约束，且host Go参考运行可以通过；六项在Darwin均有LLGo fresh compile-link-run绿色证据。2026-07-25又在4 GiB硬限制的Linux/ARM64 Go 1.26.5 + LLVM 19环境中fresh验证`timer`、`file`、`syscall-file`、`syscall-pipe`和`tcp`，Linux/AMD64 CI已验证`time`。较早的fleet checkpoint还对TCP完成8路并发共10,000次执行。它们只证明各自冻结的垂直用例，不等于完整`time`、`os`、`syscall`、`net`或GOROOT兼容。

| 验收程序 | 冻结的可观察语义 | 当前证据与缺口 |
| --- | --- | --- |
| `time` | 普通 `time.Sleep(200*time.Millisecond)` 至少延迟 150 ms | 真实Go 1.26 stdlib源码已经完成effect plan、compiler lowering、object compile、native link和运行，状态为0且无额外输出。Sleep已切到compiler-owned Timer V2 typed recipe；native/wasm32/CoroSplit、owner、cancel和race定向测试同时通过 |
| `timer` | `NewTimer`/`After`、Stop/Reset active结果与旧generation抑制、Ticker drop/Stop、AfterFunc、timer channel同步可见`len/cap`、双timer `select`和context deadline | promoted wrapper由全局physical identity、结构ABI和确定性SSA body证明后按`linkonce_odr`唯一物化，重复符号阻塞已经消除。真实Go 1.26 stdlib探针现已完成plan、compile、link和run，状态为0且无额外输出；这是上述冻结用例的E2E证据，不外推为全部GOROOT timer race、GC、`asynctimerchan`或`synctest`兼容 |
| `syscall-file` | 标准库固定导出的`syscall.Open/Write/Seek/Read/Close/Unlink`封装完成一字节回环；不经过`os.File` method/reflect表面 | 当前compiler/emission节点已用真实Go 1.26 stdlib源码fresh compile-link-run通过，证明这些fixed-target wrapper的自动染色、worker scalar result和调用者同步风格回环闭合；动态`RawSyscall`/未取得精确证书的trap仍fail closed，不外推其他syscall族或进程/信号语义 |
| `syscall-pipe` | `syscall.Pipe`后main G阻塞读；另一G经过50ms timer后写入；读至少等待25ms并收到精确字节 | Darwin与Linux/ARM64均已fresh compile-link-run通过，直接证明`Read`由worker等待且executor仍能运行timer/writer。实现同时补齐`C.pipe`的声明级foreign contract和type-patch方法值接收者ABI；两者都是通用metadata/lowering修复，不是fixture白名单。更广pipe/close/cancel矩阵仍由CI/GOROOT继续验收 |
| `file` | `OpenFile ->` 单次一字节 `Write -> Seek -> Read -> compare -> Close`；不混入deadline、poll或slice growth | 当前compiler/emission节点已用真实Go 1.26 stdlib源码fresh compile-link-run通过，证明该冻结路径上的`os.File`/`internal/poll`/regular-file worker封装闭合；deadline、目录、并发close及完整`io/fs`/reflect矩阵仍待验收 |
| `tcp` | TCP loopback的Listen/Dial/Accept、读写；静态及并发修改read/write/accept deadline；Read与Host write在途时Close；dial timeout/context cancel；UDP `ReadFrom/WriteTo`与`ReadMsg/WriteMsg`往返；pure-Go hosts及DNS wire解析 | Go 1.26同步源码已在Darwin与Linux/ARM64双owner native fresh compile-link-run通过；`wasm-unknown`和`wasip2`也由显式Node embedding fresh build、零undefined并运行完成。ARM64验证同时固定了cgo plain `char`可按目标映射为`int8`或`uint8`的统一conversion ABI，非8位指针仍拒绝。host路径的功能场景原账本为114个operation、17次exact cancellation、18个alarm和171个schedule action；现在又顺序创建/关闭80个listener，增加480个operation并使最终账本成为594/17/18/651，确定性越过64槽固定poll descriptor页并证明Close后可持续复用。功能场景覆盖Worker-only与Worker+Timer ParkSet、动态deadline重配、Close物理ack、Accept/Connect、RecvFrom/SendTo、RecvMsg/SendMsg、resolver读取受控nsswitch/resolv/hosts文件，以及并发A/AAAA UDP DNS wire查询。该并发查询曾暴露preemptible poll descriptor扫描重复分配`runtimeCtx`，现由target-lowered原子CAS保留槽且以源码gate固定；确定性stale-epoch探针还验证snapshot与bind之间的deadline/Close变更必经Submit、exact Cancel、terminal ack和同步超时返回。`runtime_pollClose`依赖标准`FD`从snapshot前持有到resume/unbind后的read/write ref，任何非closing或control lane未idle的最终close现在fail-stop而不再静默泄漏。native使用真实内核socket/文件，host保留可控的在途write/connect强竞态。早期native TCP另有8路10,000次压力。仍未证明外部DNS服务器/cgo resolver、Unix/raw socket、非空ancillary OOB、FD大量并发及完整`net`/GOROOT矩阵，也不代表WASI command已有内建reactor |

TCP探针在取得首个全绿结果前依次暴露并修复了四类通用compiler边界，而没有加入`net`函数白名单：slice-to-array pointer显式fault、runtime静态section span的受限pointer-to-uintptr证明、`unsafe.Sizeof/Alignof`未求值SSA与physical/plain helper分流，以及`defer`捕获命名结果时cleanup continuation所需heap cell的CoroSplit支配关系。最后一项只把`RunDefers`后terminal result重建精确使用的、已具备managed `AllocZ`能力的entry heap cell放到`coro.begin`和frame publish之后、initial suspend之前；普通entry、条件和循环heap allocation仍留在source位置。native与wasm32的CoroSplit前后验证、无重复分配和同cleanup owner负例均已覆盖。

#### Go 1.26 `time` linkname ABI

目标基线是 Go 1.26.5。不能只实现 `Sleep`；`time` 包和 runtime 之间的以下 ABI、`Timer`/`Ticker` 公开前缀布局与回调签名必须作为一个版本化整体验证：

| Go 1.26 symbol | 精确逻辑签名 | 契约 |
| --- | --- | --- |
| `time.now` | `func() (sec int64, nsec int32, mono int64)` | target wall clock + monotonic clock leaf；不是 suspend point |
| `time.runtimeNow` | `func() (sec int64, nsec int32, mono int64)` | synctest bubble 中返回 fake wall time且 `mono==0` |
| `time.runtimeNano` | `func() int64` | 普通执行返回 monotonic ns，bubble 中返回 fake clock |
| `time.runtimeIsBubbled` | `func() bool` | 精确反映当前 G 的 synctest bubble |
| `time.Sleep` | `func(ns int64)` | `ns<=0` 立即返回；其他情况挂起当前 G 至少 `ns` |
| `time.newTimer` | `func(when, period int64, f func(any, uintptr, int64), arg any, cp unsafe.Pointer) *Timer` | runtime 分配具有 `Timer`/`Ticker` 相同公开前缀的对象；Go runtime 物理实现使用 `*timeTimer`/`*hchan`，但跨包 ABI 必须与 `time` 声明一致 |
| `time.stopTimer` | `func(*Timer) bool` | 原子判定旧 generation 是否仍 active，并与正在发送/启动的事件同步 |
| `time.resetTimer` | `func(*Timer, when, period int64) bool` | 撤销旧 generation、安装新绝对 deadline/period，返回旧 generation 是否 active |

LLGo 可以用 controlled timer operation + managed manager G 实现这些 symbol；物理 clock/driver 只发布 completion，不得在 target callback 中运行任意 Go `f`。但“签名存在”不等于语义完成，还必须通过下表。

当前`Sleep`和controlled Timer manager wait都由compiler-owned Timer V2 typed recipe负责opaque frame storage与park/resume transaction。每个`Timer`/`Ticker`/`AfterFunc`的active loop由一个managed manager G所有，它们共用manager实现；Stop/Reset通过exact `(controller, generation)` V2 cancel使旧等待失效。controlled V1 prepare/park/cancel/retire ABI和它的symbol-specific frame-retention contract已删除。这只完成传输、取消与所有权迁移，不能据此宣称完整`time`兼容。

#### Go 1.26 timer 语义矩阵

| 能力 | Go 1.26 必须保持的语义 | 统一模型中的实现位置 | 当前垂直切片 |
| --- | --- | --- | --- |
| `Sleep` | `d<=0` 立即返回；其他情况不早于 monotonic deadline 恢复；不占用调用者 native stack | 一个compiler-owned typed park recipe；runtime仍使用统一的ParkSet、timer source、result lease与cancel，resume时exact cleanup/recycle | native/wasm32 lowering、CoroSplit、source owner、cancel/shutdown和race定向验证通过；真实Go 1.26 `Sleep`探针已compile-link-run通过 |
| `NewTimer` / `After` | one-shot；`After(d)==NewTimer(d).C`；发送的时间表示应到期时刻，包含 delayed delivery 的 `delta` 校正 | stable controller + generation；到期后由 managed G 调用 `sendTime` | controlled Timer V2定向事务测试及真实stdlib探针中的NewTimer/After、双timer select和context deadline均通过；尚未跑完整GOROOT语义/race矩阵 |
| `Timer.Stop` | active 才返回 true；默认 channel timer 在 Stop 返回后不能再收到旧值；func timer 返回 false 时 callback 已启动，Stop 不等它结束 | control generation CAS/cancel，与 channel send/callback-start 边界串行化 | exact-generation、prepare-publication race、shutdown定向测试及真实stdlib探针中的active/stale-generation行为通过；callback-start完整竞态矩阵仍待补齐 |
| `Timer.Reset` | 返回旧 timer 是否 active；默认 channel timer 不能在 Reset 后收到旧 generation；func timer 的 false 路径允许新旧 callback 并发 | cancel old generation + install new generation，旧 completion 只能 stale/discard | 旧generation取消、re-arm和真实stdlib探针通过；并发Reset/Stop完整语义与race suite仍未覆盖 |
| `AfterFunc` | `C==nil`；`f` 在独立 G 运行；Stop/Reset 不隐式 join callback，Reset(false) 可与上一次 `f` 并发 | timer completion 只 enqueue managed callback G，executor/clock callback 不直接运行用户函数 | manager-G、独立callback G和真实stdlib基本路径通过；callback重叠/Stop/Reset完整竞态矩阵仍待补齐 |
| `Ticker` / `Tick` | `d<=0` 的 New/Reset panic，`Tick` 返回 nil；慢 receiver 丢 tick 并按 period 追上；Stop 不 close channel；Reset 后下一 tick 从新 period 计 | repeating generation，到期计算 `when + period*(1+delay/period)`，非阻塞 send | repeating manager、drop/Stop基本行为和真实stdlib探针通过；Reset与完整竞态语义suite仍待补齐 |
| timer channel | Go 1.26 底层创建 cap-1 channel；默认模式对 `len/cap` 和 receive/select 呈现同步 cap-0 语义，延迟到 receiver 需要时才入 heap，Stop/Reset 可撤销 stale send | channel runtime 与 timer controller 的双向关联，block/unblock/select 钩子，send generation fence | `timerSync`/`MarkTimerChannel`已把cap-1私有存储对`ChanLen/ChanCap`呈现为0，真实stdlib探针也验证同步可见值与旧值抑制；lazy scheduling、receiver/select完整双向钩子和GOROOT竞态矩阵尚未闭环 |
| GC | 默认模式和 `asynctimerchan=2` 下，未 Stop/未到期但已不可达的 Timer/Ticker 可回收；mode 1 保留 pre-Go 1.23 不可回收行为 | source/controller 不得对 channel timer 建立无条件强 root；weak reachability 与 slot detach 需与 collector 集成 | 尚未完成；当前 per-timer manager G 持有 timer，不能声称 Go 1.23+ 可回收语义 |
| `GODEBUG=asynctimerchan` | `0`：默认 sync-visible + GC-able；`1`：legacy cap-1/stale-possible + uncollectable，`syncTimer` 向 runtime 传 nil；`2`：runtime-aware、GC-able，但 channel 呈现 async cap-1；运行中修改影响已存 timer channel | runtime debug state、channel len/cap/send 和 GC/root 策略共同分派 | 尚未完成；不能把 `cp==nil/non-nil` 单独当作完整实现 |
| `synctest` | bubble 独立 fake clock/timer set；只在 bubble durable-blocked 时推进到下一 deadline；Now/Nano/IsBubbled、timer channel、Stop/Reset 必须归属同一 bubble；callback G 继承 bubble 并建立 HB；`asynctimerchan!=0` 拒绝 `synctest.Run` | fake-clock source shard + G bubble identity + channel/sema/notify durable-blocked 统计 | 未实现；当前 `runtimeIsBubbled` fallback 为 false |

#### `os` regular file 与 `net` TCP 链路

Native regular file 不能当成 readiness source：POSIX `poll` 会把 regular file 持续报告为 ready，这会把真实的 blocking I/O 隐藏成 executor 忙转。当前设计链是：

```text
os.OpenFile / File.Read / File.Write / File.Seek
  -> internal/poll.FD
  -> stdlib 预分类，或 pollOpen 用 fstat 将 regular file/directory 拒绝为 EOPNOTSUPP
  -> runtimeCtx==0，SetDeadline -> internal/poll.ErrNoDeadline
  -> 标准 syscall wrapper / llgo.syscall ForeignOp
  -> 预留 bounded Worker slot + 在当前 coroutine 建立 result record
  -> Park
  -> 固定 4 个 native worker 从 1024-slot ring 执行可阻塞 syscall
  -> {r1,r2,errno} sticky publication + doorbell
  -> owner executor reconcile result -> resume 同步 Go caller
```

worker 不为每个 operation 建 pthread，不保留调用者 native stack，不访问 G/frame/LLVM handle。容量预留、late completion、shutdown join 和 result ownership 仍按通用 operation contract 处理；当前 1024 是 native profile 的显式上界，不是无界背压实现。

TCP socket 保持 nonblocking；`EAGAIN` 后的等待走 readiness，不占用一个 worker 阻塞等 fd：

```text
net.TCPListener/TCPConn
  -> netFD -> internal/poll.FD -> nonblocking syscall
  -> EAGAIN -> internal/poll.runtime_pollWait(ctx, 'r'|'w')
  -> Poll source {fd, interest, absolute monotonic deadline, generation}
  -> Park
  -> route-local POSIX poll set: doorbell + active fd directions + earliest timer/deadline
  -> readiness | timeout | closing sticky result
  -> internal/poll retry syscall or map to ErrDeadlineExceeded / ErrClosed
```

当前通用 `llgo.syscall` coroutine lowering 仍可能把每次短小的 nonblocking syscall 本身交给 worker；这不会用 worker 等待 readiness，但会带来不必要的 handoff。target-neutral `NonblockingLeaseGate`核心已能以exclusive attempt和generation transition覆盖SetBlocking/close/reuse竞态，callable层也已有target-owned TrustedInline exact-edge闭环；尚缺的是把两者通过`internal/poll`受控wrapper和operand proof连接。连接后nonblocking socket leaf可在当前resume episode直接执行；这仍是优化，不改变 `EAGAIN -> Poll -> Park -> retry`语义链。

deadline 修改必须更新已 park operation，且 timeout 恢复后重新读取当前 deadline，才能拒绝已被清除或延后的 stale timeout。`Close` 经过两组关联唤醒：

1. `fdMutex.increfAndClose` 设置 closed，通过 coroutine semaphore 唤醒 read/write lock waiters；
2. `pollDesc.evict -> runtime_pollUnblock` 向 read/write Poll slot 发布 closing，已 park I/O 返回 `net.ErrClosed`；
3. 最后一个 FD reference 销毁 descriptor 并 `Semrelease(csema)`，`Close` 的 `Semacquire` 在确认无 I/O 再使用 fd 后返回。

`sync.Once`、`sync.Cond`、FD mutex 和 close barrier 所需的 semaphore/notifyList 也必须 park logical G，不能退回在任一 executor thread 上等 pthread cond。当前 keyed-wait namespace 已分开 semaphore 与 notify ticket，冻结的native双owner TCP标准库探针已经通过上述链路；完整FD并发、close竞态、动态P迁移和GOROOT矩阵仍待整体验收。

#### 平台边界

| 平台 | 模型映射 | 2026-07-29 production 现状 |
| --- | --- | --- |
| Native Linux/Darwin | POSIX monotonic clock + 每route pipe doorbell/`poll` reactor + 共享bounded pthread worker | opt-in fleet profile已接入program target：启动固定8个M/P route，环境/online CPU初始化逻辑execution quota，标准`runtime.GOMAXPROCS`可运行期调整managed-resume并行上限；program start/stop、全部peer join、共享4-worker/1024-job MPMC ring、Timer/Poll/Worker/Channel exact route和强关闭均已闭环。双owner TCP stdlib probe及10,000次压力、execution quota race、真实1→4→1并发和同步stdlib入口通过。无指针demand和exact mailbox可通用转移P-neutral initial/yield/materialized-park runnable，并以Imported门防止首次运行前回弹；owner P现以一个Try transaction转移约一半、最多8个runnable，只发一次target request。locked-M同P replacement已覆盖foreign、普通Yield/channel/timer Park、compiler-poll preempt、socket poll和nested handoff，clean succession覆盖三类未解锁owner退出，command main-return blocked-M也已闭环。temporary M由8项standby cache复用，统一physical-thread ledger覆盖program/factory/worker/peer/replacement/standby，标准`runtime/debug.SetMaxThreads`已接通并通过stdlib与上限对抗E2E。跨target完整affinity、callback/reentry与非command shutdown、GC/语言/GOROOT矩阵仍未完成，因此不是完整Go runtime兼容 |
| JS/WASM | 1P `RunSlice` 返回 host，Promise/timer/requestRun 投递 POD fact | host-owned pull ABI、generation/epoch和capability profile已完成；命名`wasm` command内建microtask/timeout pump，真实Go 1.26普通`syscall/js`值操作、墙钟/时区/`time.Sleep`已运行。显式Node embedding另对`wasm-unknown`和`wasip2`驱动file及TCP/UDP HostOp、动态deadline和取消；command pump尚未实现非空HostOp、FuncOf/HostReentry，不能外推为任意Promise/source兼容 |
| WASI | 1P + pollable/poll_oneoff 或对应 preview API | Preview 1普通command已内建单线程、自有memory的bounded reactor：initial slice返回后消费公共action/operation POD，以一个`poll_oneoff`集合等待absolute alarm和fd read/write，并由Wasmtime运行并发timer及`syscall.Open/Read/Write/Seek/Close/Unlink`。Preview 1没有标准socket create/connect，网络明确`ENOSYS`；`path_open`/unlink/metadata没有标准pollable形式，当前同步host import只声明Degraded compensation，不能保证慢host filesystem期间其他G前进；高层`os.File`还受通用interface exact-effect缺口阻挡。Preview 2仍只是core-module/layout target，其pollable/component entry lifecycle需要外部reactor，不能把Preview 1证据外推为完整WASI或标准库E2E |
| RTOS/embedded | 静态 P/source catalog + notification/event queue + one-shot alarm + ISR POD ingress | compiler已选择仅一次initial slice且detached返回的host-target V2 entry，callback roots已保留；HAL notification/alarm、ISR source、持久reactor pump与capacity配置仍由embedding实现，不能称E2E |
| baremetal | 1P main loop + IRQ ring + hardware alarm + WFI/WFE | host-target V2 initial entry、allocation-free notification/alarm profile、POD action与strong-join core可交叉编译，callback roots已保留；真实IRQ ring、hardware compare、WFI/WFE reactor/startup和GC集成未实现，不能称E2E |

Native表中的有界fleet已从production-island推进为可执行target profile；它证明无栈continuation可在固定8-route物理topology上按动态逻辑quota并行，并能用统一event/worker模型运行标准库TCP链。物理route/source identity不随`GOMAXPROCS`变化；locked-M同P replacement已证明阻塞期间原route继续服务channel/timer/poll并可嵌套，clean succession又证明未解锁退出不会复用被污染M，但当前证据仍不能外推为全部可运行G迁移、所有callback/reentry/非command shutdown/affinity场景或完整标准库兼容。

无栈 continuation 和 target-neutral operation core 不依赖 libuv、BDWGC 或 pthread；但当前 native worker adapter 确实使用固定 pthread pool，collector 集成也仍是 target profile 的独立责任。LLVM 支持基线固定为 22；LLVM 21 及以下不再维护兼容配置或 CI。LLVM 22 必须验证 CoroSplit、frame layout/root metadata 和 module verification。

full-native Darwin/Linux 的 signal adapter 已改为 C `sigaction` + nonblocking self-pipe；signal handler 只发布 lock-free POD signum，Go `signal_recv` 复用 coroutine poll owner，不依赖 legacy libuv timer loop。当前 source/C/race 验证已通过，`os/signal.Notify` 的真实 LLGo 链接执行仍随全程序 acceptance 推进。

### 9.2 历史 Phase 22–32 审查基线

相对 `xgo-dev/llgo` merge-base `2c9d1897`，Phase 22 head `072622208` 的物理新增行为：

| 类别 | 新增行 | 说明 |
| --- | ---: | --- |
| Compiler/analysis/build | 25,313 | `internal/coro`、`cl`、`ssa`、build/link和target支持 |
| Runtime | 9,028 | scheduler、wait/executor、ABI glue、doorbell、allocator、timer |
| Tests/fixtures/test adapters | 39,501 | 包括race、E2E和negative proof |
| Documentation | 2,826 | 总体设计 |
| CI | 244 | coroutine gates |
| 合计 | 76,912 | 另有528行删除；为physical diff，不是去空行后的SLOC |

现有实现符合预期的部分：

- Effect、Exec、Demand、FuncRep和Primary已经分开。
- 固定点能把 `llgo.coroPark` 的MayPark沿静态普通调用自动传播。
- 静态 DirectCoro child await、typed result slot和LLVM frame handoff是通用路径。
- runtime已有统一`ParkState/OperationID/WaitSetRecord`状态机、executor request gate和doorbell防丢唤醒基础；旧G logical wait queue已经删除。
- 每个函数只选择一个primary body；普通静态路径不生成完整双版本。

以下是Phase 22 head当时尚未达到核心完成条件的部分；其中已变化的项目同时标出当前head边界：

- Phase 22的descriptor codegen只有受限plain V1。当前head已硬切FuncRep v2，覆盖dynamic child await、plain/outcome/coroutine capability、closure/capture、method以及receiver-aware open/closed managed interface dispatch；archive summary也能让consumer为外部Dispatch producer生成唯一descriptor和typed薄thunk。Native `reflect.Value.Call/CallSlice`、`MakeFunc`、绑定方法值及timer挂起已通过typed descriptor/libffi边界运行。仍未完成的是完整open-world publication/function-leaf schema、WASM/baremetal AOT reflect trampoline、runtime未知签名、`reflect.Select`以及完整dynamic/GOROOT矩阵。
- package Summary明确不是producer archive ABI；独立预编译标准库的effect传播尚无最终contract。
- Phase 22的physical coroutine lowering仍是pure-SSA子集。当前head已覆盖method/closure/generic的多个管理路径、常用builtin、slice/aggregate、implicit fault和指针provenance；但完整variadic/recursive/defer/recover/Goexit/cleanup、所有runtime helper和全部Go语言矩阵仍fail closed或待验收。
- suspended frame没有精确GC root map和write barrier contract。
- Phase 22的Sleep frame retention曾按timer符号和SSA形状硬编码；当前Sleep、controlled Timer manager wait和Poll wait已分别收敛为compiler-owned Timer/Poll V2 typed recipe与opaque frame storage。controlled Timer V1专用ABI和frame-retention contract已删除；`Timer`/`Ticker`/`AfterFunc`虽已走V2 transport/core，仍不得由此外推完整time语义。
- Phase 23及最终hard cutover已将ExecutorDriver的bind/publish/pending/deadline/empty/close/unbind收口到静态`ExecutorSourceSet`，并把source fact publication与logical resolution分开：active Poll固定执行有界epoch A并立即resolve/promote、ack request、再无条件执行同构epoch B，B后不等待pending/request静默；`IdleArmed` final scan发现事实则先离开idle再重跑完整transaction。Timer、Poll、Manual、Worker、Channel和TaskControl各自维护binding-local单调`scanLimit`，routine epoch只扫描曾分配的active prefix，成功unbind后清零；Configure、close、empty、CanRelease和unbind仍审计完整configured capacity。空多页目录的事务从native默认配置的16403 reductions降为3；真实Sleep/Timer二进制指令量分别下降98.39%/98.33%，语义验收保持通过。旧logical wait source和Timer/Poll双模式已经删除；winner lease未Take/Discard前仍不能recycle。
- Phase 23已将每个G run slice的scheduler service budget与active timer解耦；host-owned pull adapter现已把WASM/embedded的`RunSlice` more/blocked/deadline转换为非递归POD action并对callback generation/epoch作精确校验，compiler host-target V2 initial entry、retained callbacks与strict detached tuple边界也已完成；外部reactor tick/sysmon请求和post-optimization safepoint上界证明仍未完成。
- Phase 23已实现V2 `OperationID/OperationRecord`和G-owned `ParkState`核心：支持多source完整sticky snapshot、与publish/source顺序无关的唯一事件winner、普通取消与task/shutdown abort竞态、败者resolution-ack/detach barrier、物理quiesce/recycle分离、结果lease、准备失败清理以及不回绕的双`u32`logical ticket。固定`CompletionSink` fact数组已经删除，owner直接扫描operation sticky facts；`ParkState`已内嵌到稳定G。该阶段首先覆盖Manual与Timer这类`IrreversibleCompletion`多事件等待；后续Phase 26/27补上`ReadyThenTryCommit/Reservable` core，Phase 32 C0再补无payload Channel claim/`TryCommit`，但legacy Wait迁移、typed hchan和Go select完整接线仍未完成。
- 执行取消已收敛为G内嵌的`Abort/Shutdown` sticky kind和`Requested/CleanupClaimed` phase；owner P可把请求映射到当前或下一次ParkState，shutdown可覆盖同一完整snapshot中的operation completion，late cancel通过每P瞬态`RunDecision` gate抑制selected continuation但保留winner result lease。固定容量`TaskControlSource`已经作为第四种source接入统一published-epoch catalog：只为显式host/export handle分配generation端点，并以占用G现有对齐空洞的owner-only lease计数阻止task storage早回收。`Goexit`已从远程task cancel kind移出。
- TaskControl registered delivery已从公开任意G的O(ready/wait/ParkLink)审计中拆出：exact endpoint在注册/park owner transition时完成结构审计，后续每个source slot只做O(1) owner/lease/scalar-header/local-head/winner-record证明，再复用同一个带proof mode的`requestTaskCancellationOwned` mutation core。长ready队列与256-candidate远端环测试证明公开API仍拒绝完整结构损坏，而exact registered delivery不读取无关tail；损坏local head或winner record仍fail closed。该成本结论只覆盖已注册TaskControl事实交付；公开取消审计、后续logical candidate resolution、legacy `PollReady`迁移扫描、park candidate构造/排序、当时尚未typed接线的Channel/Poll/Host以及完整ready/resume/destroy `RunSlice`仍各自需要界定或认证。当前head已有typed hchan、Poll V2、host-owned pull核心和host-target V2 initial command entry，但完整production route、外部reactor接线与cost certificate仍未闭环，不能据此宣称所有scheduler路径已是O(1)。
- runtime已具备V2 Prepare/Waiting/Ready/Checked/Take、exactly-once scalar resume ABI；compiler所有现有initial/child-await/yield/legacy-park/bootstrap resume都先消费exact resume gate。zero-ticket和Channel/Worker/Timer/Poll恢复路径现可把`Abort/Shutdown`保留为frame-local terminal base，进入共享cleanup，并通过parent-owned `CompletionRecord`跨child destroy向多层祖先传播；普通defer返回不覆盖该base，panic/recover作为独立overlay处理。当前工作树又加入了frame-rooted异构动态defer LIFO：循环注册、typed descriptor/context/argument record、pop/copy/free-before-call、可挂起descriptor child以及取消恢复的五状态reconciliation均通过native/wasm32 pre/post CoroSplit定向验证；implicit language fault也能在drain前无分配物化payload并被direct defer recover。所有compiler-facing transition hook和`Resumed`仍要求当前P/G的exact resume gate已经被取走，并在claim、publish或消费调度状态之前拒绝漏取。尚未完成的是range-over-func跨frame `DeferStack`、`Goexit`、完整panic/defer语言矩阵、其他legacy wait、全部Go Timer语义以及外部host reactor pump。
- 取消路径没有每G外部registry、callback链或独立executor；普通G的control lease为零且不增加G尺寸。source admission容量仍由各target静态catalog负责，embedded/baremetal和未来multi-P还需要证明统一的slot/queue bound与endpoint迁移协议。
- `OperationID`已冻结为两字`source:8 + route:9 + local:15 + generation:32`；route在runtime instance内单调分配且永不复用，关闭后留下永久tombstone，Manual/TaskControl/Poll/Worker producer可只凭POD ID投递精确executor，Timer V2的record/lease也使用相同exact route且由owner时钟驱动。当前`ExecutorFleet`、program executor原位收养、Timer/Poll/Worker route、固定8-route物理M owner/stop/join、动态逻辑execution quota、共享worker lifecycle、route-local reactor、locked-M replacement、普通locked Yield/Park/compiler-preempt、三类owner succession、有界standby M与统一`SetMaxThreads`账本均已接入native target。零/单Timer、Manual、Poll、Worker以及typed Channel/select、单Worker HostOp、Worker+Timer deadline和keyed/private-registry park的`parkReady`现由旧owner物化到P-neutral `ResumePacket`后才可迁移；需求claim、exact mailbox和Imported门开放了通用P-neutral global injection/work sharing。owner-only intrusive queue现在用精确计数一次迁移约一半、最多8个continuation，并只请求一次target；其他P不读取victim link，因而不新增并发deque状态机。仍未完成的是跨target完整affinity以及callback/reentry与非command shutdown矩阵，不能由有界profile外推为完整native multi-P。
- frame-local`WaitSetRecord`、独立V2 active双链与affected FIFO已经替代V2 `PollReady`全waiting扫描；record-aware attach/mark/detach/promote为O(1)，一次resolution扫描其C个candidate。1024-candidate测试通过破坏远端节点证明fast detach没有隐藏全链审计。production apply已按resolved batch逐candidate静态分派到source `ApplyOne`，不再扫描Manual/Timer全容量；后续大容量source必须保持该复杂度。
- Phase 26/27已把commit-capable select core和common published-epoch resolver收敛为同一个allocation-free状态机。`ReadyThenTryCommit`绑定logical ticket、exact `OperationID`和单调readiness generation，失败只消费该hint并从下一个rank继续；`Reservable`逐candidate commit/rollback；ordinary cancel、strong cancel和default共用唯一terminal decision与physical acknowledgement/detach barrier。兼容同步wrapper只循环驱动同一bounded primitive，不再保留第二套`published -> winner -> disposition`逻辑。后续Phase 32 C0接入无payload Channel，当前head又已接入typed hchan与Poll V2 exact fleet route，并跑通冻结的native双owner TCP探针。完整Go select语义、host source binding和完整netpoll矩阵仍未闭环。
- Phase 27已使固定source catalog和common wait-set resolution全路径有界：A/B各source slot、ack、affected wait-set、rank scan、Ready `TryCommit`、candidate settle、`ApplyOne`、finish、promotion及legacy-G visit都保存owner-only cursor并各计一个reduction；`budget=1`可持续前进，且snapshot跨host entry由`ParkState.resolving`冻结。`RetryBudget`保持`more`，`AwaitExternalFact`离开affected queue并等待新sticky fact，二者不会制造无事件忙转。这里完成的是executor transaction的source/common-resolution部分；ready-G dequeue/resume/destroy、inline-ready wrapper和连续child await尚未纳入同一wall-work slice，因此完整`RunSlice`仍未完成。
- Phase 29已把operation result lifetime冻结为`Empty/Owned/Leased/Taken/Discarded`单字节状态，替换原来的`resultConsumable/resultTaken`且保持`OperationRecord`为64-bit 80 bytes、32-bit 60 bytes。Irreversible/Reservable publication建立`Owned`，Ready hint保持`Empty`，只有exact `BindParkCommitResult`可生成成功attempt；Manual、Timer和exact fake source都按“source cleanup/rollback -> loser Discard -> Ack”执行，winner在Consume时取得lease并由Take或Discard结束。late task cancellation保留lease供cleanup Discard，stale/duplicate lease和未绑定Ready success均fail closed。当前零/单Timer、Manual、Poll、Worker已把该lease事务接到frame-local `ResumePacket`；typed channel、多source和完整逐frame reconciliation仍是后续工作。
- Phase 30已在不改变`OperationRecord/G/P/ParkState`布局的前提下加入source-owned scalar payload core。28-byte V1 POD和36-byte exact-ID cell支持0..3个逻辑`uint64`，公共事务API覆盖Irreversible/Reservable publication、Ready bind、winner Take/Discard及loser clear-before-Ack；invalid Meta、duplicate/lost/stale generation、Ready失败重发、Take-vs-Discard、32-bit word encoding和零分配均有定向覆盖。单source物化为给`WaitSetRecord`增加一个可选frame-local packet指针（native 56 bytes、WASM32 32 bytes），G/P/ParkState/OperationRecord布局不变；Worker payload在旧owner复制到52-byte pointer-free packet。typed Go pointer/channel值、普通Go `error`对象和多source operation reconciliation仍需独立plan。
- Phase 31把普通single-P执行路径接到同一个可续账本：`ExecutorRunStep`只产生budget-one source reduction、ready dequeue+`BeginRunG`、一个完整物理action或稳定idle/terminal receipt；runner直接调用私有budget-one poll primitive，不再经`PollExecutor/PollReady/NextRunnable`。公开兼容入口`PollExecutorSlice{At}`在`sourceMore/readyDebt/blocked/issued`任一cursor状态非零时原子拒绝，必须先从stable idle显式调用`EnterExecutorRunCompatibility`，因此不能绕过hot-source fairness debt。runtime adapter把`done + Checked + resume + Resumed`或`done + Checked + destroy + DestroyedBounded`作为不可拆的一个physical reduction，随后把live continuation重新排到FIFO尾；连续2048层同步child await因此是迭代的2048个resume action，不会在一个host entry内递归跑完。这里的“一个physical reduction”只定义不可返回的原子边界，并不证明resume期间执行的compiler/runtime hook具有常数成本。每个G用原有对齐空洞中的`runAction`保存三种live continuation，32/64位G大小保持168/288 bytes。只有compiler冻结的command bootstrap direct `CoroRoot` handoff与normal-main-return后的final root destroy可以前插：每个bootstrap表项最多前插一次child destroy和一次exact root resume，nested child仍保持FIFO；main-return marker发布后只剩一个final root destroy，Go退出语义禁止其间再启动用户G。完成的A/ack/B必须先结束，`readyDebt`再强制hot source开始下一epoch前执行一个ready physical action。
- Phase 31b已加入host-facing V2 slice ABI：`__llgo_coro_program_run_slice_v2`和`__llgo_coro_program_continue_slice_v2`只通过固定32-byte、8个`uint32`的POD result返回status、used、exact executor tuple、epoch和deadline；V1/V2模式在首次进入时冻结，V2每个entry只推进指定budget并在回到host后才允许非递归`requestRun`，同步回调被折叠为`Repost`，不能在同一机器栈递归重入。native Linux/Darwin compiler entry使用固定机器栈循环和`budget=1024`，只接受canonical `Complete`或精确`Yielded + More|RequestInline`，其余状态或畸形字段fail closed。WASM/WASI、embedded和baremetal的compiler entry现也选择V2：只执行一次`budget=1024`的initial slice，保留`next_action/profile/next_deadline/publish_time/ack_cancel/continue/post_wait`等callback reference roots，对canonical `Complete`或精确`Yielded + More|RequestQueued`、`Suspended + Blocked[+HasDeadline]` tuple作严格校验。`Yielded`/`Suspended`验证后不inline loop、不递归re-entry；generic embedding仍在此处detached返回host。命名JS command由仓库Node pump消费该协议，WASI Preview 1 command则在initial slice完全返回后进入固定容量C reactor并用`poll_oneoff`驱动continuation；Preview 2、embedded和baremetal仍由外部embedding负责。
- TaskControl在`CheckDestroy/PanicDestroy`已排队后交付的sticky `Requested`不能先于cleanup销毁目标frame：带非零`runAction`的G不能由公开owner API提前`Claim`成Cleanup；`BeginRunG`在dequeue提交前拒绝两种queued destroy并由runner原样恢复queue；`CheckDestroy`的`done`门再次检查owner在dispatch后插入的request，只有无request时才签发`ActionDestroy`；`ActionDestroy`签发后owner API不再接受新token。`PanicDestroy`通过首道门后已进入`GPanicking`，owner取消API不接受该状态、source又只能在idle P服务，且physical action无host boundary，所以不需要另建preflight对象。compiler cleanup lowering完成前，被拒绝的token、target frame、handle和queue保持可诊断，不伪造ack或硬清。
- command main正常返回还必须覆盖ready tail上尚未执行的child physical continuation：shutdown显式消费`CheckResume/CheckDestroy/PanicDestroy`，从现有suspended chain或destroy target直接进入cancel destroy，绝不重复`done/resume/destroy`。若main-return marker先于child panic报告完成，则Go进程退出语义胜出；child的panic record保留到全部frame销毁后再由command cancellation丢弃，不能提前丢GC root或把panic误报为普通child完成。
- Phase 31的post-resume scheduler commit和普通root destroy只检查O(1) queue header/local state；最后一个frame释放后，`P.current`保留handle-free `ActionCommitDestroy` receipt，`g.root/destroyTarget`和旧handle均已清除，receipt永不进入ready queue。旧whole-episode driver在单独标明的compatibility边界执行full audit、terminal executor close或legacy schedule CAS；该边界不制造synthetic handle。仍未纳入production cost bound的是physical resume内部的`findFrame`/`validPanicAncestry`、`PrepareParkSet` link scan与`SealParkSet`排序，idle prepare/wake、terminal/command close与shutdown、frame registry扫描/`Zero`、TaskControl endpoint delivery的legacy owner-membership队列扫描、select preparation与multi-source materialization cost certificate、非native target的外部reactor pump与平台event-source接线、post-optimization cost certificate和通用多P；因此这里只证明source cursor、dispatch、单source packet promotion和resume后的scheduler commit可续有界，不能宣称所有reduction或所有source路径已经strict cost-certified。
- Phase 32 C0已把无payload Channel source加入production静态catalog和route binding，并迁移到共享`producerSourceSlot/routedProducerSource` lifecycle；它实现4-byte `SelectClaim`、commit-domain discovery、owner-held `TryCommit`、yield-epoch `RetryBudget`、external forced winner、`Ready` drain被forced单调超越、A-after forced的exact FIFO恢复、Abort/Shutdown forced-result discard以及detach/quiesce/result/recycle分层。producer external commit先持有两端pre-effect exact-ID admission，admission覆盖全部frame claim访问并延续到两端`Claimed`之后；Closing不能丢已admit事实，Apply不能越过held lifetime lease，泛型单ID route不会提前替Channel请求doorbell。定向测试覆盖stale-Ready CAS重读Forced、paused drain、seal夹在admission/effect之间、held admission阻止detach/promotion、claim contention/mismatch、bounded runner真实`readyDebt -> Dispatch`公平性、stale/duplicate、default/ordinary/strong cancel、forced begin无隐藏全链扫描和budget=1 A/ack/B。该阶段没有typed hchan/payload/compiler lowering，也没有claim-less single-case external fence，不能宣称Go channel/select已完成。
- callable/resource边界已加入无分配、固定布局的`NonblockingLeaseGate`核心：exact resource、capability和generation绑定一个exclusive bounded attempt；BeginChange先撤销Active并seal admission，ChangeQuiesced后才允许owner修改OS/HAL/host状态，FinishChange发布新generation，FinishRetire留下永久tombstone。定向测试覆盖错resource/capability、并发attempt、duplicate/copy release、transition-held lease、reuse和retire。该原语不持有G/frame且不得跨suspend；`internal/poll.FD`、SetBlocking和read/write wrapper尚未接线。

因此Phase 22应视为首个可运行vertical slice，而不是“核心已经完成后新增一个timer功能”。

## 10. 实现优先级与验收门槛

2026-07-22的执行顺序是“核心能力优先，少量标准库程序只作定位探针”：

1. `StringCat`、泛型`llgo.index`、Darwin sysctl bridge、visibility-only `go:linkname`、`*ssa.MakeMap`及promoted managed wrapper唯一发射阻塞已经越过；真实`time`、`timer`、`syscall-file`、`syscall-pipe`、`file`与`tcp`探针已在同一native fleet acceptance全部fresh compile-link-run通过，TCP另有10,000次压力证据。下一步把该组固定为架构等价重构门，同时保持“只修generic core、不写package/function白名单”的门槛。
2. `ExecutorFleet`的程序接入、固定8-route M/P owner/stop/join、动态逻辑execution quota、标准`runtime.GOMAXPROCS`、worker lifecycle、route-local reactor、Timer/Poll/Worker exact route，以及零/单source、Channel/select、HostOp deadline和keyed/private-registry park的typed `ResumePacket`物化已完成；通用P-neutral demand injection/work sharing、owner-assisted bounded half-batch、locked-M同P replacement、普通locked Yield/Park/compiler-preempt、三类owner succession、command main-return blocked-M终止、有界standby M缓存和标准`runtime/debug.SetMaxThreads`也已完成。下一并行核心是跨target完整affinity以及callback/reentry与非command shutdown矩阵；每个尚未证明P-neutral的park继续禁止迁移。并发local deque只保留为profiling驱动优化，不作为正确性gate。
3. 以已完成的host-owned pull core、host-target V2 entry、JS/WASM command timer pump、WASI Preview 1 timer/file command reactor和显式Node file/network runner为边界，把同一operation协议接入JS command HostOp/HostReentry、WASI Preview 2 pollable/component lifecycle、RTOS HAL和baremetal IRQ/alarm/WFI loop；Preview 1低层文件证据不升格为network、高层`os.File`或完整WASI E2E。
4. 在已闭环的静态task-stop cleanup/`CompletionRecord`子集上，完成动态defer、完整panic/recover、`Goexit`、P-neutral result以及post-LLVM bounded-preemption/GC证明。
5. 上述核心门稳定后，在已通过的六个冻结探针上继续扩展`test/*`、`net`/`os`/`time`与GOROOT语义矩阵，不反向把标准库特例写进compiler core。最终仓库门是`test/*`和Go 1.26 GOROOT全部适用测试通过、无unexpected failure和stale XPASS；六个探针只是快速定位门。

以下P0–P3保留为契约checklist，不表示每一项都仍未开始；完成状态以上述当前垂直验收和平台矩阵为准。

### P0：统一runtime core

1. 抽取 `SourceSet` 和统一scan/idle/shutdown transaction。
2. 将当前等待cell放入稳定G或operation record；定义scalar `OpID`。
3. 拆分 `DetachWaiter` 与 `RecycleSourceSlot`。
4. 实现G-owned `WaitSet`、`Won/Lost/Invalid` claim和loser cancel/detach barrier。
5. 实现分层执行取消：request、logical terminal、detach和quiesce。
6. 用第三种fake/manual source验证executor不再按source分支。
7. 将抢占请求与timer解耦，并固定P/M/global injection ownership。
8. 在Phase 31已完成的普通source cursor、dequeue/dispatch和post-resume scheduler commit账本及Phase 31b V2 POD slice ABI/host-target initial entry上，先切分或认证physical resume内部的frame/ancestry/link scan与select排序，再纳入idle prepare/wake、terminal/command close、shutdown、frame scan/Zero和仍可能隐藏工作的source-specific wrapper；实现各host target的外部reactor pump与平台event-source接线，并完成post-LLVM cost certificate，继续严格区分`RetryBudget`和`AwaitExternalFact`。
9. 实现commit-capable select：`ReadyThenTryCommit`携带exact readiness generation，`Reservable`携带exact reservation generation，失败或stale只消费对应hint；`default`只能在本轮所有candidate均给出不可提交证明后选择，logical winner后的physical commit/rollback acknowledgement仍属于promotion barrier。
10. 在已完成的显式result ownership/lease协议和静态task-stop `CompletionRecord`闭环上接入真实typed payload、动态逐frame cleanup与`Goexit`：每次resume先按exact ticket reconciliation并复制后Take或直接Discard结果，再进入normal continuation或`Return/Panic/Goexit/Abort/Shutdown` cleanup。当前`Abort/Shutdown`不能再标作“仅fail-closed”，但也不能把静态cleanup证据外推为完整Go unwind。

### P1：完成公共source、P-neutral并行与容量协议

1. Timer table已是公共source contract，当前使用bind前配置、bind期间冻结的固定分页catalog及binding-local active-prefix保证迁移与routine scan成本正确；Sleep与controlled Timer manager的Timer V2 current-owner/typed recipe、真实`time`/`timer`探针均已完成。剩余工作是timer channel完整lazy/select联动、GC/`asynctimerchan`、`synctest`以及Stop/Reset/Ticker/AfterFunc的完整GOROOT race/semantic suite，不是controlled transport迁移。
2. 再升级dynamic/sharded heap和Go Timer/Stop/Reset/Ticker/AfterFunc语义。
3. Native、WASM/WASI、RTOS和baremetal只实现各自clock/alarm/wait adapter；target-neutral host-owned pull core和compiler host-target V2 initial entry已完成，JS command timer pump与WASI Preview 1 timer/file reactor也已接线。剩余是JS HostOp/reentry、WASI Preview 2 pollable/component lifecycle、RTOS HAL和baremetal IRQ/WFI平台source glue，不为它们复制scheduler。
4. compiler中controlled Timer V1的symbol-specific frame retention已删除；Sleep与controlled Timer wait都使用typed recipe，并以negative source/contract tests防止旧ABI和特判回归。
5. 零/单Timer、Manual、Poll、Worker以及Channel/select、Worker+Timer deadline和runtime-private registry/transport已经通过固定`ResumeCleanupKind + POD plan`在进入可迁移ready状态前物化到compiler/runtime提供的P-neutral `ResumePacket`，并结束原route result/cleanup lease。fleet demand injection只接受该P-neutral证明；新P不得回访旧source取得payload或执行cleanup，未覆盖的新source/park默认继续禁止迁移。
6. 为worker、netpoll、host和静态RTOS/baremetal source定义统一admission/backpressure结果：`Accepted | RetryBudget | AwaitCapacity | Unsupported`。`AwaitCapacity`使用generation稳定的source fact并支持cancel-before-start；任何容量都遵守reserve-before-publish，不能静默丢请求或退化成每operation线程/对象。

以上机制使用紧凑record、标量identity和静态source catalog实现；其他语言的`Future`、`Task/Job`、`Promise`、sender/receiver对象图、STM retry log或每G mailbox都不进入Go ABI或每G常驻布局。

### P2：补齐compiler core

1. 通用suspend-region/operation-record lowering和GC frame metadata。
2. `RuntimeCapabilityCatalog`集中管理target capability、runtime roots、ABI signatures和contract IDs。
3. `PackageCoroSummary`作为真实archive ABI。
4. 用真实stdlib探针持续验收通用physical lowering与emission ownership；`*ssa.MakeMap`和promoted managed wrapper全局唯一发射均已越过，`time`、`timer`、`syscall-file`、`syscall-pipe`、`file`与`tcp`均已形成compile-link-run证据。继续以更广`test/*`、`net`/`os`/`time`及GOROOT测试定位下一项通用缺口，不由这些探针外推完整语言或标准库兼容。
5. 在已有descriptor、dynamic child await、method/interface/closure/generic和native reflect typed-call路径上，补齐open-world archive、非native reflect trampoline、全部dynamic call矩阵以及defer/recover/Goexit/cleanup语义。
6. 完成source-independent bounded preemption proof。

### P3：统一模型上的I/O

1. netpoll/readiness source；Poll V2 typed recipe、opaque descriptor、exact fleet route及native双owner TCP E2E/压力已通过；host complete-operation路径又在双WASM target覆盖TCP/UDP、动态deadline、Close、Accept/Connect、RecvFrom/SendTo和RecvMsg/SendMsg，pure-Go resolver已通过文件HostOp读取hosts及并发A/AAAA UDP DNS wire查询。外部DNS服务器/cgo resolver、Unix/raw socket、ancillary OOB、FD复用/大量连接和完整netpoll矩阵仍待完成。
2. completion/worker ForeignOp source；已有bounded worker/core、scalar result、route-aware fleet transport、并发producer MPMC ring、program-level fleet lifecycle及`syscall-file`/高层`file`真实E2E证据；完整syscall族、排队背压和取消/容量矩阵仍待完成。
3. 标准库 `internal/poll`、`os`、`net` 和 syscall family的同步风格接入。
4. 验证 effect 自动染色所有上层caller，不维护async源码分叉。

### 扩展成本门槛

底层核心完成后，新增一个普通异步wrapper应满足：

- effect analysis、CallPlan和static await生产代码零修改；
- compiler中不出现该API的symbol name；
- runtime core状态机通常零修改；
- 只增加source submit/result adapter、平台adapter和标准库薄入口；
- 测试行数不设上限，但必须覆盖early completion、cancel race、stale generation、shutdown和目标平台；
- 若新增第三/第四种source仍需复制executor idle/drain/close逻辑，则核心设计未通过验收。
