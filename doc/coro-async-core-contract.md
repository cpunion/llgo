# LLGo 统一异步执行核心与扩展契约

状态：设计冻结前的实现审查稿

更新：2026-07-23

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

一个 descriptor 可以发布 plain primary、coroutine primary或consumer生成的薄 adapter capability，但不得包含两份 source body。

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

### 3.3 Lowering matrix

| Caller | Callee/值 | Lowering |
| --- | --- | --- |
| plain | plain direct | 普通 direct call |
| coroutine | plain direct | 当前 resume episode 内普通 direct call |
| coroutine | coroutine direct | 创建/取得 child continuation并 structured await |
| coroutine | descriptor | 检查 capability；plain slot直接调用，coro slot创建 child并 await |
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

Child的frame可能在parent恢复前销毁，因此非普通终态不能只留在child header或G的一次性cancel token。structured await使用parent-owned、release/acquire发布的versioned `CompletionRecord`保存`Return/Panic/Goexit/Abort/Shutdown`、panic identity和用户result；parent先读取kind，只有`Return`才读取普通result。root终态同样在destroy前复制到稳定boundary/G storage。现有cleanup-free terminal panic和直接command destroy只能保留为证明无defer/recover的受限快路径，不能作为通用Go unwind。

当前工作树已完成可独立闭环的task-stop和动态cleanup子集：`CompletionRecord`实际发布并逐层消费`Return/Panic/Abort/Shutdown`，另用`ReturnRecovered`提交direct deferred invocation的recover；frame-local terminal status使普通defer返回不会把`Abort/Shutdown`覆盖成`Return`，defer中的panic作为overlay优先，只有精确的`ReturnRecovered`会清除原panic。zero-ticket gate以及Channel/Worker/Timer/Poll/child-await恢复路径都会保留精确cancel kind；取消命中await后仍先消费child completion，并按五种状态重新进入同一个drainer。函数一旦存在循环注册，全部defer site统一进入frame-rooted异构LIFO，typed record在调用前pop/copy/free；无环site（包括可闭合的descriptor defer）仍保留一次注册的typed slot fast path。当前证据覆盖compiler可枚举的owner-local defer与direct recover；range-over-func的跨frame`DeferStack`、`Goexit`、root boundary outcome及完整GOROOT panic/defer矩阵仍未完成。

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

当前native fleet profile已经把这条core接入真实程序：route 1原位收养command线程上的program P，route 2由一个固定pthread M拥有；两个domain各有独立P/driver/source catalog、pipe doorbell和POSIX poll set，并并行执行同一个物理reducer。固定8槽`RunnableTransferMailbox`接受never-run root、普通`SuspendYield`以及已进入`parkMaterialized`的`SuspendPark` continuation；slot以generation和FIFO exact import持有唯一GC root。G复用既有对齐padding中的一字节`transferState`，Published期间关闭pointer-only preempt gate并拒绝普通`Enqueue`，因此不会被第三个P重复取得，且native/wasm32的G大小不变。当前分配策略只机会性转移每次physical resume产生的第一个新spawn；mailbox争用或满时保留本地FIFO。它证明双M/双P并行、启动/停止/join、初始任务迁移和受限parked-result迁移可运行，但尚不是动态GOMAXPROCS、通用global run queue或任意runnable work stealing。

2026-07-26的第一阶段物化接受零或一个Timer、Manual、Poll、Worker source。`WaitSetRecord`可绑定compiler/runtime提供的52-byte、align-4、pointer-free frame-local `ResumePacket`；原owner在`parkReady` promotion事务中先Consume winner lease，再按source类型Take/Discard payload、确认quiescence并回收exact generation，最后把`ParkState`压缩为不含winner/source ownership的`parkMaterialized`。Worker标量和Poll枚举在这里复制，Timer/Manual不保留payload；迁移后的resume只消费packet，prompt task cancellation也只抑制packet，不回访旧source。packet-bound wait不能走缺少`ExecutorSourceSet`的legacy promotion入口，旧`parkReady/parkConsumed/parkDelivered`仍被迁移gate拒绝。

第二阶段已把direct Channel和multi-case channel `select`接到同一机制。compiler-spilled `ResumeCleanupPlan`只保存固定`ResumeCleanupKind`、typed runtime context、`{entries,count,stride,idOffset}` ID range、source/claim和阶段游标；没有callback、interface、函数地址反查或永久G字段。common resolver在旧P上Consume exact decision后停在`ExecutorRunStepMaterialize`，runtime direct switch每次处理一个logical case：从hchan队列摘除typed waiter、完成必要的buffer/close reconciliation、复制winner的封闭`waitStatus`，并清除frame中的hchan/source/claim指针，只暂留exact ID。随后core逐ID执行`ConfirmQuiesced`，统一reset共享claim、Take/Discard winner lease，再逐ID Recycle并清零；最后才写入P-neutral packet、转成`parkMaterialized`并进入ready queue。direct、两case winner/loser、close、task cancel、native race/shuffle、JS/WASM、旧P source为空后跨P迁移，以及迁移后prompt task cancel均已覆盖。真正发生的Yield/Park会确认已经由本次suspension满足的G-local preempt request；executor/source request仍有自己的sticky gate，因此不会因残留`preemptRequested`误拒合法迁移，也不会丢服务义务。

第三阶段把typed materialization内部的hchan reconciliation进一步拆成frame-local runtime子游标。每个`ExecutorRunStepMaterialize`现在至多摘除当前case waiter或处理一个peer waiter；buffer receiver、buffer refill、closed receiver和closed sender各有显式phase，phase-only前进也单独占一个reduction。hchan mutex不会跨runner返回保留，select resume也不再线性清零case；core recycle清掉最后的ID后，resume只消费packet并清共享state。native与JS/WASM测试以64个discard/commit peer验证任一步queue变化不超过一个节点。该证明只界定materialization的peer数量；单个channel element的固定上限copy成本、park preparation/sort、普通try路径的discard扫描以及同步`close`全队列drain仍需各自的cost gate，不能扩大表述为完整hchan已全路径常数成本。

第四阶段已把单Worker HostOp和Worker+Timer deadline接到同一个typed plan。`RuntimeCount`与物理source `Count`正交：HostOp只产生一个固定枚举runtime materialization step，在旧owner上退休host adapter及control transport；随后common core分别确认Worker/Timer quiescence、取得winner payload、回收exact generation并清零frame ID。Timer先胜而Worker仍在执行时，resolver保留`AwaitExternalFact`，直到物理取消确认到达才恢复promotion，不制造无事件忙转。Worker标量直接写入已有52-byte `ResumePacket`，96-byte native/72-byte WASM32 plan不重复保存payload；恢复后不再调用worker driver、run decision或旧owner transport。实际WASM host-owned file与带deadline network探针均从Go同步调用风格完成。

第五阶段已把semaphore/notify keyed park的runtime-private registry接到同一plan。frame先绑定完整cleanup descriptor，再把POD key/generation发布到registry；旧owner的唯一runtime step退休exact registry generation，common core随后确认、消费或丢弃并回收route-local Manual source，resume只取packet。`Posting`竞态不再循环或新增scheduler stop：producer在Manual source Post前先把private slot发布为`Delivered`；若取消先退休Posting generation，producer观察retired并跳过Post；若Delivered后Post并发，Manual source自身的producer admission、close和quiescence决定Post成功或stale，frame和G都不会暴露给producer。标准库opaque state冻结为pointer-aligned 256 bytes，实际runtime布局为native64 256 bytes、WASM32 196 bytes。native/WASM race/layout、generic Manual P-neutral core以及真实同步风格`Mutex + Cond + WaitGroup`程序均通过。该结论只消除了resume旧owner依赖与Posting忙转；固定2048槽registry的`claimOne`仍在短NoSuspend gate内作有界线性选择，后续容量/高争用优化不能被表述为已完成O(1)。

两字`OperationID`已经用稳定`RouteID`解决多P source namespace：两个P的同类source可以都从local slot 1开始，但callback必须携带完整`{source, route, local, generation}`，通过route admission精确发布source fact并请求对应executor。P teardown先seal route并strong-join producer，旧route留下永久tombstone；该路由约束不允许重新引入Go pointer callback ABI或按fd/函数地址反查owner。

推荐的V2编码保持两字布局：`word0 = source:8 | route:9 | local:15`，`word1 = operationGeneration:32`。`route`是runtime instance生命周期内单调分配且不复用的`RouteID`，route close后留下永久tombstone；local slot和operation generation都从1开始，generation不回绕。这样只凭POD ID即可O(1)找到owner source，不引入per-operation全局目录，并保留完整32-bit热slot generation。超过511个lifetime route或每route/source超过32767个live slot的profile必须选择versioned wider/flat-directory ABI并明确内存代价，不能偷占generation bits。

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
`link identity + physical symbol + structural ABI`的域隔离证书。它承诺一次调用可在任意worker线程执行、在worker发布完成前同步返回、不回调或重入managed Go、参数按值传递，且返回前不保留任何Go pointer；它不承诺短延迟或nonblocking。函数地址先进入`uintptr` carrier的路径使用正交的`workeraddr <arity>`前向事实，consumer不得再从地址反查symbol或策略。两类指令都与`noblock`、`sync`、`schedulerwait`互斥；SSA plan保留普通`ExternalUnknownForeign + BlockForeign + WaitForeign`语义，不能因证书而弱化异步染色。worker lowering必须先验证closed call、精确producer及目标宽度下可无损word-pack的参数/结果，再要求plan和frozen emission universe两侧证书完全一致；任一侧缺失、identity不等、ABI不支持或普通未标注foreign declaration都fail closed。当前审计目录已覆盖本轮文件/TCP链所需的固定Darwin file/socket/address-resolver/sendfile等leaf以及少量runtime leaf，不再只有`C.write`；fork/exec、线程控制、未知ioctl/kevent/ptrace和未审核目标仍保持raw/plain隔离或拒绝。像`gai_strerror`返回进程期C指针的特殊结果由独立foreign-pointer result contract声明，不扩大普通worker scalar ABI。

Linux共享`RawSyscall` carrier还要求与函数地址证书正交的trap证书。精确I/O常量和只读credential query（`CAPGET`、`GETUID/GID/EUID/EGID`、`GETRESUID/GID`、`GETGROUPS`）可使用worker；当前支持域内credential mutation仍fail closed，固定worker fleet继承同一启动credential。未来启用Go的all-thread credential mutation前，必须把同一事务扩展到全部fleet worker，不能提前外推兼容性。动态trap、credential mutation、thread-affine和process-control仍fail closed。标准库vfork/clone child专用的`forkAndExecInChild1`与`doCheckClonePidfd`由source annotation取得`RawCritical`双实体，managed caller只在该精确call occurrence选择原生栈plain variant；不能把`EXIT/EXIT_GROUP`整体误声明为worker-safe，也不为标准库函数名增加lowering白名单。

## 9. 当前实现审查

### 9.1 2026-07-25 当前垂直验收

本节记录当前工作分支的垂直切片，不覆盖下文保留的 Phase 22–32 历史审查。验收硬边界是：

- 用户程序和 Go 标准库必须保持普通同步 Go 源码风格；只允许使用 `time.Sleep`、`os.File.Read/Write`、`net.TCPConn.Read/Write`、`go`、channel 等标准语法和 API。
- 验收 fixture 不得 import LLGo/LLVM 实现包，不得出现私有 `Future`、`Await`、`Task` 或显式 callback 改写。底层 wrapper 变成 `MayPark` 后，由 effect 固定点自动染色所有 managed caller。
- 只有“使用 LLGo 编译普通标准库源码，并且编译、链接、运行与可观察语义全部通过”才算 E2E 通过。host Go 运行成功、source-selection 测试、runtime 单元测试或手写 runtime driver E2E 都不能替代这个门槛。
- OS/host I/O、timer、同步原语及其他外部等待不得占住executor；必须先stack-cut并park，或转交bounded worker/平台event source。每一种接入都要有等待尚未完成时另一G仍能继续运行的进度测试。当前允许纯计算暂时占用一个P；生产级并行在P-neutral result/runnable之后以M/P/G、动态P/steal和blocking compensation解耦。

核心能力已有以下可运行/可编译证据，用来确认compiler、coroutine frame、executor和source契约能在真实链接边界上闭环：

- native no-stdlib runtime island已链接运行channel、static spawn与closed-channel/send-closed fault路径；
- explicit panic已验证child payload和parent propagation的production链接执行；
- native timer已与CPU hot loop同时运行，由compiler safepoint、有界`RunSlice`和timer due drain完成公平唤醒，hot loop不需要显式yield；
- `time.Sleep`已改为compiler-owned Timer V2 typed park recipe；定向lowering/CoroSplit、current-owner、cancel/shutdown与race验证已通过，不再依赖按timer符号猜测frame retention；
- 受控Timer manager的wait/Stop/Reset已迁移到compiler-owned Timer V2 typed recipe和exact `(controller, generation)` park/cancel/resume；旧controlled V1 prepare/park/cancel/retire ABI、build root和symbol-specific frame-retention特判已删除，exact cancel、prepare-publication race、winner lease和shutdown-before-deadline定向验证已通过。这些只证明transport/ownership core，不代表完整Go Timer语义已通过；
- `internal/poll.runtime_pollWait`已改为compiler-owned Poll V2 typed park recipe；定向lowering、current-owner retained reactor、deadline/closing/cancel/shutdown与race验证已通过。外部Node embedding现已在`wasm-unknown`与`wasip2`真实运行标准库同步TCP/UDP fixture，覆盖HostOp提交、alarm、动态deadline、Close取消和物理ack；这仍不等于普通command自带reactor；
- production `TaskControl`端点已通过register/post/close、exact executor request、safepoint claim与terminal竞态测试；
- allocation-free `ExecutorFleet`核心、route registry和P-neutral runnable transfer已通过core/race测试并接入native程序入口；route 1由command线程执行，route 2由固定pthread M并行执行，两者共用同一物理reducer并各自拥有P/source catalog/doorbell/reactor。普通空domain通过同一`IdleArmed -> final scan -> CommitSleep`事务进入standby，由routed request精确唤醒；启动、shutdown publication、route strong-join、peer join和driver/source回收已形成完整target lifecycle。除无source-affinity的新spawn/yield外，单Timer/Manual/Poll/Worker park结果现可在旧owner物化后迁移；multi-source及typed hchan cleanup仍留在原route；
- native poll descriptor已从共享Go map/fd反查改为每FD一个C分配的opaque scalar owner；read/write方向各原子保存deadline和完整`OperationID`，park/close/deadline update通过seq_cst handshake精确投递route。owner同一轮同时观察到到期deadline与已排队readiness时由deadline胜出；deadline reset/remove通过frame-retained snapshot与当前descriptor重检后重新注册。双owner TCP probe已fresh compile-link-run，并完成8路共10,000次压力而无deadline/readiness误序；
- managed heap allocation已在native和wasm32通过compile/CoroSplit、spill/reload、exact-root profile与zero-size sentinel测试；
- coroutine string concatenation已收敛为对`runtime.StringCat`的精确structured-outcome lowering，overflow panic沿显式结果传播，native/wasm32 compile、CoroSplit与object emission已通过；
- slice到array pointer/value转换的`N>0`长度失败已进入同一explicit-status fault/recover路径，`N==0`保持nil与empty-non-nil slice的原始data pointer语义。operand-free fault继续使用allocation-free V1静态payload；需要运行时操作数的转换失败使用正交V2 payload/prepare ABI，把目标array长度和源slice长度作为两个target-width word直接从lowering传入，并在异常路径分配GC可见的`boundsError`。其动态类型、`runtime.Error`分类和Go 1.26逐值错误文本均已由runtime contract以及GOROOT `convert4.go`的真实compile-link-run验证。V2的两个hook、精确签名和ABI identity已经进入required-root、bootstrap digest与physical ABI hash；未知kind、operand不匹配或缺失V2 runtime一律fail closed，不退回丢失参数的通用文本。
- 泛型intrinsic已能从精确instance解析origin和SSA body，`llgo.index`在native/wasm32均按inline-no-suspend语义lower，不伪造可调用函数体；
- Darwin已补齐`internal/cpu`两个sysctl bodyless声明的精确runtime bridge和C sync certificate；另外，一参数、不重定向的visibility-only `go:linkname`已冻结为独立证书，paired bodyless consumer只选择managed `$coro`入口，不生成raw plain body。

这些是core-first证据，不是标准库兼容性证明。native compiler command entry仍是固定机器栈V2循环，但target start现已把program P/driver/source/registry作为route 1原位收养，同时创建route 2并启动其pthread owner；program return会停止并join peer与共享worker pool，再按route、backend、driver顺序强关闭。Timer catalog、Poll V2 callback与Worker transport均使用exact route：Timer由各domain owner的单调时钟扫描；Poll和Worker的两字`OperationID`在route admission内完成source publication、exact executor request、duplicate/stale分类和关闭强汇合；Worker复用一个进程级固定物理池。单source result物化/迁移已经越过，尚未完成的是multi-source typed materialization、动态P数量、普通global injection/steal和thread-affinity策略。target-neutral host-owned pull adapter已完成core/race与JS/WASM、WASIp1、baremetal、显式embedded交叉编译验证：`more`只发布later-turn action，idle只暴露POD executor/generation/epoch/deadline，alarm/notification取消后才可复用epoch，shutdown seal两个ingress并strong-join callback tail。compiler的host-target V2 entry也已完成：它只执行一次有界initial slice，保留`next_action/profile/next_deadline/publish_time/ack_cancel/continue/post_wait`等callback reference roots，仅接受canonical `Complete`或executor slot/generation/epoch/flags/deadline均精确的`Yielded`/`Suspended` tuple，后两者detached返回host，不在entry内继续调度或递归re-entry。仓库Node runner现已作为显式embedding真实消费这些action，并在`wasm-unknown`/`wasip2`运行file与TCP/UDP标准库fixture；普通JS/WASM或WASI `_start`仍没有内建reactor，因此不能把该受控embedding外推为通用platform E2E。JS command pump、WASI `poll_oneoff`、RTOS HAL与baremetal IRQ/WFI glue、完整cleanup/defer/recover/Goexit lowering以及precise/moving GC仍是明确缺口。

执行器已收口到一套物理run-step reducer；fleet外层只保留exact domain owner、显式单调时钟和budget，不存在第二套resume/destroy/commit语义。空fleet P使用公共timer-aware idle事务完成standby admission；提交后先释放owner epoch，route-local固定poll set执行fault-containment有界物理pass，doorbell/fd/deadline唤醒后再取得新epoch并执行公共`WakeExecutorAt`，只完成idle→active并设置`sourceMore`，所有source解析和typed materialization继续由同一run-step reducer完成。idle prepare/commit中若request或事实源赢得竞争，也执行相同的“leave idle + sourceMore”转换，不再嵌套调用legacy全量poll。普通domain最后一个G销毁只完成该G并保持executor可接收未来routed work；command route的main-return语义仍由program coordinator独占。实际peer M循环、共享worker pool lifecycle、分布式shutdown和强关闭引用清理现均已接入并通过production-island、source/race及TCP E2E验证。

Worker transport的route-aware核心现已完成：仍只有一个进程级固定4线程/1024-job物理池，C11 sequence ring允许多个exact P owner并发预留不同cell；每个reservation在compiler-enforced no-suspend hook内submit或以tombstone cancel。Job不增加指针或全局operation目录，直接使用既有两字`OperationID.SourceSlot`中的route；完成端按compiler-reserved target profile静态选择legacy program或fleet ingress，fleet再完成exact source/request/doorbell投递。owner在把完成发布为可resume winner前seal generation并等待所有已admit producer退出，避免回调release尾与result recycle竞态。C11环已通过route 1/2并发producer、交错wrap、四consumer和stop/join测试；program-level fleet coordinator实际start/stop该池，当前fleet标准库profile不再退回single-P worker transport。

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

| 平台 | 模型映射 | 2026-07-22 production 现状 |
| --- | --- | --- |
| Native Linux/Darwin | POSIX monotonic clock + 每route pipe doorbell/`poll` reactor + 共享bounded pthread worker | opt-in fleet profile已接入program target：command M/route 1与peer pthread M/route 2并行运行独立P/source catalog，program start/stop、peer join、共享4-worker/1024-job MPMC ring、Timer/Poll/Worker/Channel exact route和强关闭均已闭环；双owner TCP stdlib probe及10,000次压力通过。当前只机会性转移安全的新spawn/yield任务；已park G、dynamic GOMAXPROCS、通用global runq/steal、完整GC/语言/GOROOT矩阵仍未完成，因此不是完整Go runtime兼容 |
| JS/WASM | 1P `RunSlice` 返回 host，Promise/timer/requestRun 投递 POD fact | host-owned pull ABI、generation/epoch、microtask/timeout capability profile及交叉编译已完成；compiler host-target V2 entry只执行一次initial slice，保留callbacks，严格校验canonical `Complete`或精确detached `Yielded`/`Suspended` tuple。显式Node embedding已对`wasm-unknown`和`wasip2`真实驱动file及TCP/UDP HostOp、timer、动态deadline和取消；普通command `_start`仍没有内建JS reactor pump，不能由受控runner外推为任意Promise/source或通用E2E |
| WASI | 1P + pollable/poll_oneoff 或对应 preview API | compiler host-target V2 initial entry、retained callbacks、strict tuple与detached return已完成；外部reactor仍必须消费`next_action`，调用`poll_oneoff`/相应pollable，发布time/ack并调用continuation。普通command尚无该pump，fd/socket source glue也未接，不能称E2E |
| RTOS/embedded | 静态 P/source catalog + notification/event queue + one-shot alarm + ISR POD ingress | compiler已选择仅一次initial slice且detached返回的host-target V2 entry，callback roots已保留；HAL notification/alarm、ISR source、持久reactor pump与capacity配置仍由embedding实现，不能称E2E |
| baremetal | 1P main loop + IRQ ring + hardware alarm + WFI/WFE | host-target V2 initial entry、allocation-free notification/alarm profile、POD action与strong-join core可交叉编译，callback roots已保留；真实IRQ ring、hardware compare、WFI/WFE reactor/startup和GC集成未实现，不能称E2E |

Native表中的双domain已从production-island推进为可执行target profile；它证明无栈continuation可在两个真实M/P间并行，并能用统一event/worker模型运行标准库TCP链。它仍不把受限双domain profile外推为任意P数量、全部可运行G迁移或完整标准库兼容。

无栈 continuation 和 target-neutral operation core 不依赖 libuv、BDWGC 或 pthread；但当前 native worker adapter 确实使用固定 pthread pool，collector 集成也仍是 target profile 的独立责任。LLVM 支持基线只是 19–22，不考虑 LLVM 19 以下版本；每个支持版本都要分别验证 CoroSplit、frame layout/root metadata 和 module verification。

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

- Phase 22的descriptor codegen只有受限plain V1。当前head已有dynamic child await、function descriptor/capability、closure/capture、method以及receiver-aware open/closed managed interface dispatch的定向覆盖，不再是plain V1-only；native `reflect.Value.Call/CallSlice`、`MakeFunc`、绑定方法值及timer挂起也已通过typed descriptor/libffi边界运行。仍未完成的是producer archive/open-world summary、WASM/baremetal AOT reflect trampoline、runtime未知签名、reflect.Select以及完整dynamic/GOROOT矩阵。
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
- `OperationID`已冻结为两字`source:8 + route:9 + local:15 + generation:32`；route在runtime instance内单调分配且永不复用，关闭后留下永久tombstone，Manual/TaskControl/Poll/Worker producer可只凭POD ID投递精确executor，Timer V2的record/lease也使用相同exact route且由owner时钟驱动。当前`ExecutorFleet`、program executor原位收养、Timer/Poll/Worker route、双物理M owner/stop/join、共享worker lifecycle和route-local reactor均已接入native target。零/单Timer、Manual、Poll、Worker以及typed Channel/select、单Worker HostOp、Worker+Timer deadline和keyed/private-registry park的`parkReady`现由旧owner物化到P-neutral `ResumePacket`后才可迁移；仍未完成的是通用global injection/steal、dynamic P policy和affinity，不能由固定双domain profile外推为完整native multi-P。
- frame-local`WaitSetRecord`、独立V2 active双链与affected FIFO已经替代V2 `PollReady`全waiting扫描；record-aware attach/mark/detach/promote为O(1)，一次resolution扫描其C个candidate。1024-candidate测试通过破坏远端节点证明fast detach没有隐藏全链审计。production apply已按resolved batch逐candidate静态分派到source `ApplyOne`，不再扫描Manual/Timer全容量；后续大容量source必须保持该复杂度。
- Phase 26/27已把commit-capable select core和common published-epoch resolver收敛为同一个allocation-free状态机。`ReadyThenTryCommit`绑定logical ticket、exact `OperationID`和单调readiness generation，失败只消费该hint并从下一个rank继续；`Reservable`逐candidate commit/rollback；ordinary cancel、strong cancel和default共用唯一terminal decision与physical acknowledgement/detach barrier。兼容同步wrapper只循环驱动同一bounded primitive，不再保留第二套`published -> winner -> disposition`逻辑。后续Phase 32 C0接入无payload Channel，当前head又已接入typed hchan与Poll V2 exact fleet route，并跑通冻结的native双owner TCP探针。完整Go select语义、host source binding和完整netpoll矩阵仍未闭环。
- Phase 27已使固定source catalog和common wait-set resolution全路径有界：A/B各source slot、ack、affected wait-set、rank scan、Ready `TryCommit`、candidate settle、`ApplyOne`、finish、promotion及legacy-G visit都保存owner-only cursor并各计一个reduction；`budget=1`可持续前进，且snapshot跨host entry由`ParkState.resolving`冻结。`RetryBudget`保持`more`，`AwaitExternalFact`离开affected queue并等待新sticky fact，二者不会制造无事件忙转。这里完成的是executor transaction的source/common-resolution部分；ready-G dequeue/resume/destroy、inline-ready wrapper和连续child await尚未纳入同一wall-work slice，因此完整`RunSlice`仍未完成。
- Phase 29已把operation result lifetime冻结为`Empty/Owned/Leased/Taken/Discarded`单字节状态，替换原来的`resultConsumable/resultTaken`且保持`OperationRecord`为64-bit 80 bytes、32-bit 60 bytes。Irreversible/Reservable publication建立`Owned`，Ready hint保持`Empty`，只有exact `BindParkCommitResult`可生成成功attempt；Manual、Timer和exact fake source都按“source cleanup/rollback -> loser Discard -> Ack”执行，winner在Consume时取得lease并由Take或Discard结束。late task cancellation保留lease供cleanup Discard，stale/duplicate lease和未绑定Ready success均fail closed。当前零/单Timer、Manual、Poll、Worker已把该lease事务接到frame-local `ResumePacket`；typed channel、多source和完整逐frame reconciliation仍是后续工作。
- Phase 30已在不改变`OperationRecord/G/P/ParkState`布局的前提下加入source-owned scalar payload core。28-byte V1 POD和36-byte exact-ID cell支持0..3个逻辑`uint64`，公共事务API覆盖Irreversible/Reservable publication、Ready bind、winner Take/Discard及loser clear-before-Ack；invalid Meta、duplicate/lost/stale generation、Ready失败重发、Take-vs-Discard、32-bit word encoding和零分配均有定向覆盖。单source物化为给`WaitSetRecord`增加一个可选frame-local packet指针（native 56 bytes、WASM32 32 bytes），G/P/ParkState/OperationRecord布局不变；Worker payload在旧owner复制到52-byte pointer-free packet。typed Go pointer/channel值、普通Go `error`对象和多source operation reconciliation仍需独立plan。
- Phase 31把普通single-P执行路径接到同一个可续账本：`ExecutorRunStep`只产生budget-one source reduction、ready dequeue+`BeginRunG`、一个完整物理action或稳定idle/terminal receipt；runner直接调用私有budget-one poll primitive，不再经`PollExecutor/PollReady/NextRunnable`。公开兼容入口`PollExecutorSlice{At}`在`sourceMore/readyDebt/blocked/issued`任一cursor状态非零时原子拒绝，必须先从stable idle显式调用`EnterExecutorRunCompatibility`，因此不能绕过hot-source fairness debt。runtime adapter把`done + Checked + resume + Resumed`或`done + Checked + destroy + DestroyedBounded`作为不可拆的一个physical reduction，随后把live continuation重新排到FIFO尾；连续2048层同步child await因此是迭代的2048个resume action，不会在一个host entry内递归跑完。这里的“一个physical reduction”只定义不可返回的原子边界，并不证明resume期间执行的compiler/runtime hook具有常数成本。每个G用原有对齐空洞中的`runAction`保存三种live continuation，32/64位G大小保持168/288 bytes。只有compiler冻结的command bootstrap direct `CoroRoot` handoff与normal-main-return后的final root destroy可以前插：每个bootstrap表项最多前插一次child destroy和一次exact root resume，nested child仍保持FIFO；main-return marker发布后只剩一个final root destroy，Go退出语义禁止其间再启动用户G。完成的A/ack/B必须先结束，`readyDebt`再强制hot source开始下一epoch前执行一个ready physical action。
- Phase 31b已加入host-facing V2 slice ABI：`__llgo_coro_program_run_slice_v2`和`__llgo_coro_program_continue_slice_v2`只通过固定32-byte、8个`uint32`的POD result返回status、used、exact executor tuple、epoch和deadline；V1/V2模式在首次进入时冻结，V2每个entry只推进指定budget并在回到host后才允许非递归`requestRun`，同步回调被折叠为`Repost`，不能在同一机器栈递归重入。native Linux/Darwin compiler entry使用固定机器栈循环和`budget=1024`，只接受canonical `Complete`或精确`Yielded + More|RequestInline`，其余状态或畸形字段fail closed。WASM/WASI、embedded和baremetal的compiler entry现也选择V2：只执行一次`budget=1024`的initial slice，保留`next_action/profile/next_deadline/publish_time/ack_cancel/continue/post_wait`等callback reference roots，对canonical `Complete`或精确`Yielded + More|RequestQueued`、`Suspended + Blocked[+HasDeadline]` tuple作严格校验。`Yielded`/`Suspended`验证后直接detached返回，不inline loop、不递归re-entry、不自行消费reactor action；因此外部embedding仍必须驱动回调和continuation，普通WASM/WASI command尚无该pump，不能称E2E。
- TaskControl在`CheckDestroy/PanicDestroy`已排队后交付的sticky `Requested`不能先于cleanup销毁目标frame：带非零`runAction`的G不能由公开owner API提前`Claim`成Cleanup；`BeginRunG`在dequeue提交前拒绝两种queued destroy并由runner原样恢复queue；`CheckDestroy`的`done`门再次检查owner在dispatch后插入的request，只有无request时才签发`ActionDestroy`；`ActionDestroy`签发后owner API不再接受新token。`PanicDestroy`通过首道门后已进入`GPanicking`，owner取消API不接受该状态、source又只能在idle P服务，且physical action无host boundary，所以不需要另建preflight对象。compiler cleanup lowering完成前，被拒绝的token、target frame、handle和queue保持可诊断，不伪造ack或硬清。
- command main正常返回还必须覆盖ready tail上尚未执行的child physical continuation：shutdown显式消费`CheckResume/CheckDestroy/PanicDestroy`，从现有suspended chain或destroy target直接进入cancel destroy，绝不重复`done/resume/destroy`。若main-return marker先于child panic报告完成，则Go进程退出语义胜出；child的panic record保留到全部frame销毁后再由command cancellation丢弃，不能提前丢GC root或把panic误报为普通child完成。
- Phase 31的post-resume scheduler commit和普通root destroy只检查O(1) queue header/local state；最后一个frame释放后，`P.current`保留handle-free `ActionCommitDestroy` receipt，`g.root/destroyTarget`和旧handle均已清除，receipt永不进入ready queue。旧whole-episode driver在单独标明的compatibility边界执行full audit、terminal executor close或legacy schedule CAS；该边界不制造synthetic handle。仍未纳入production cost bound的是physical resume内部的`findFrame`/`validPanicAncestry`、`PrepareParkSet` link scan与`SealParkSet`排序，idle prepare/wake、terminal/command close与shutdown、frame registry扫描/`Zero`、TaskControl endpoint delivery的legacy owner-membership队列扫描、select preparation与multi-source materialization cost certificate、非native target的外部reactor pump与平台event-source接线、post-optimization cost certificate和通用多P；因此这里只证明source cursor、dispatch、单source packet promotion和resume后的scheduler commit可续有界，不能宣称所有reduction或所有source路径已经strict cost-certified。
- Phase 32 C0已把无payload Channel source加入production静态catalog和route binding，并迁移到共享`producerSourceSlot/routedProducerSource` lifecycle；它实现4-byte `SelectClaim`、commit-domain discovery、owner-held `TryCommit`、yield-epoch `RetryBudget`、external forced winner、`Ready` drain被forced单调超越、A-after forced的exact FIFO恢复、Abort/Shutdown forced-result discard以及detach/quiesce/result/recycle分层。producer external commit先持有两端pre-effect exact-ID admission，admission覆盖全部frame claim访问并延续到两端`Claimed`之后；Closing不能丢已admit事实，Apply不能越过held lifetime lease，泛型单ID route不会提前替Channel请求doorbell。定向测试覆盖stale-Ready CAS重读Forced、paused drain、seal夹在admission/effect之间、held admission阻止detach/promotion、claim contention/mismatch、bounded runner真实`readyDebt -> Dispatch`公平性、stale/duplicate、default/ordinary/strong cancel、forced begin无隐藏全链扫描和budget=1 A/ack/B。该阶段没有typed hchan/payload/compiler lowering，也没有claim-less single-case external fence，不能宣称Go channel/select已完成。
- callable/resource边界已加入无分配、固定布局的`NonblockingLeaseGate`核心：exact resource、capability和generation绑定一个exclusive bounded attempt；BeginChange先撤销Active并seal admission，ChangeQuiesced后才允许owner修改OS/HAL/host状态，FinishChange发布新generation，FinishRetire留下永久tombstone。定向测试覆盖错resource/capability、并发attempt、duplicate/copy release、transition-held lease、reuse和retire。该原语不持有G/frame且不得跨suspend；`internal/poll.FD`、SetBlocking和read/write wrapper尚未接线。

因此Phase 22应视为首个可运行vertical slice，而不是“核心已经完成后新增一个timer功能”。

## 10. 实现优先级与验收门槛

2026-07-22的执行顺序是“核心能力优先，少量标准库程序只作定位探针”：

1. `StringCat`、泛型`llgo.index`、Darwin sysctl bridge、visibility-only `go:linkname`、`*ssa.MakeMap`及promoted managed wrapper唯一发射阻塞已经越过；真实`time`、`timer`、`syscall-file`、`syscall-pipe`、`file`与`tcp`探针已在同一native fleet acceptance全部fresh compile-link-run通过，TCP另有10,000次压力证据。下一步把该组固定为架构等价重构门，同时保持“只修generic core、不写package/function白名单”的门槛。
2. `ExecutorFleet`的程序接入、双M owner/stop/join、worker lifecycle、route-local reactor、Timer/Poll/Worker exact route，以及零/单source、Channel/select、HostOp deadline和keyed/private-registry park的typed `ResumePacket`物化已完成。下一并行核心是开放通用global injection/steal、动态P数量和affinity；每个尚未证明P-neutral的park继续禁止迁移。
3. 以已完成的host-owned pull core、host-target V2 entry和显式Node file/network runner为边界，把同一action/continuation协议接入普通JS/WASM command pump、WASI `poll_oneoff`/pollable reactor、RTOS HAL和baremetal IRQ/alarm/WFI loop；受控runner证据不升格为这些平台的内建启动/runtime E2E。
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
3. Native、WASM/WASI、RTOS和baremetal只实现各自clock/alarm/wait adapter；target-neutral host-owned pull core和compiler host-target V2 initial entry已完成，实际外部reactor/HAL/IRQ pump与平台source glue仍待各平台接线。
4. compiler中controlled Timer V1的symbol-specific frame retention已删除；Sleep与controlled Timer wait都使用typed recipe，并以negative source/contract tests防止旧ABI和特判回归。
5. 零/单Timer、Manual、Poll、Worker已在进入ready queue前物化到compiler/runtime提供的P-neutral `ResumePacket`并结束原route result lease，且通过跨P resume与迁移后prompt cancellation验证。继续用固定`ResumeCleanupKind + POD plan`覆盖Channel/select、Worker+Timer deadline及runtime-private registry/transport；完成前不得进入通用global injection或work stealing，新P不得回访旧source取得payload或执行cleanup。
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
