# LLGo 统一异步执行核心与扩展契约

状态：设计冻结前的实现审查稿

更新：2026-07-17

关联总体设计：[`llvm-coro-runtime-design.md`](./llvm-coro-runtime-design.md)

## 1. 结论

LLVM coroutine 只负责保存、恢复和销毁无栈 continuation。它不是异步模型，也不应知道 timer、文件、网络或某个 syscall 的语义。

LLGo 的异步能力必须先形成一套与 continuation backend 解耦的公共核心：

1. 编译器以 effect 固定点决定函数是否可能挂起，以独立的 value-flow 决定函数值是否需要动态描述符，以 demand 决定入口是否需要物化。
2. Scheduler 只管理逻辑 `G`、可运行队列、park/wake、取消、抢占和生命周期。
3. Executor 负责把可运行 `G` 映射到执行资源，并执行统一的 drain、idle admission、sleep、wake 和 shutdown 协议。
4. Event source 管理 timer、I/O、foreign worker、host Promise、RTOS notification 或 IRQ 等外部事实。
5. Platform adapter 只提供 monotonic clock、doorbell、阻塞/返回 host、线程或中断接入等目标能力。

完成这套核心后，`time.Sleep`、文件、网络和绝大多数标准库同步风格 API 应只增加 source/adapter 或薄 runtime wrapper。新增普通异步能力不得再次修改 effect 传播、静态 coroutine call lowering 或 timer 专用的 frame 证明。

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

Timer、channel、netpoll 和普通异步 wrapper 复用 `Park`。`time.Sleep`、每个 fd API 或每个 syscall number都不能成为新的 compiler opcode。

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

`WaitTicket`只标识一次G的逻辑park；`OpID`只标识一个物理source slot。两者不合并，也不把Go指针编码进identity。对外ABI在未验证所有32-bit目标的alignment之前，`OpID`保持显式的两个`u32` POD word，不直接依赖Go `uint64`布局。

关键不变量：

- Arm 在 operation 对 producer可见之前完成，early completion不能丢失。
- Park 只把一个 exact generation绑定到一个 G。
- Complete 与 Cancel只有一个 terminal winner。多候选等待的claim结果必须能区分`Won`、`Lost`和`Invalid`，`Lost`不是runtime corruption。
- `DetachWaiter` 在 G重新进入 ready queue前清除 source 对 G/frame/wait cell的全部访问能力。
- `RecycleSourceSlot` 只有在 producer unregister/join 或其他strong quiescence后才能复用 slot generation。
- Timer 没有外部 producer，due drain时可以同时完成、detach和recycle。
- fd/host/worker operation可以先形成 pointer-free tombstone，等待backend quiesce后再recycle。

一个 G 同时只会因一个逻辑 wait进入 `GWaiting`。稳定G中应内嵌完整`ParkState`，而不只是一个临时`WaitToken`；它至少包含ticket、phase、outcome和wait-set reference。ticket generation的wrap/reset只能在不再有raw token pointer逃逸、所有关联source都已detach后发生，不得依赖29-bit计数器fail-stop作为正常运行策略。

`select` 可以注册多个source candidate，但它们共享同一个 G-owned winner cell；loser在winner确定后取消并完成detach barrier，之后winner才可以ready。Detached/background operation使用独立 operation record，不占用 G 当前 wait cell。

### 4.3 多候选 select 与执行取消

这两项是scheduler/operation core的基础能力，不是netpoll或某个API的特例。

`select` 使用一个稳定`WaitSet`：

- 一个owner G、一个logical ticket和一个原子winner cell；
- 多个candidate，每个持有独立`OpID`、case index、result record和detach phase；
- ready的candidate只尝试claim winner，不直接唤醒G；
- 败选candidate返回`Lost`并进入cancel/detach，不得当作stale/corruption；
- 所有loser达到detached或pointer-free tombstone后，executor才把winner对应的G放入ready queue；
- 已具备多个ready case时，在不破坏Go伪随机选择语义的前提下选winner，不由source扫描顺序偷偷决定。

取消是分层协议，不是一个boolean：

1. `CancelRequested`：已将请求durable publish，但completion仍可能已经获胜。
2. `LogicalCanceled`：logical wait/wait-set已选定cancel outcome，G仍不一定可ready。
3. `Detached`：source已不再能访问G、frame、winner cell或Go result pointer，此时才可ready/reclaim G。
4. `Quiesced`：backend已unregister/join，旧callback不再可能进入，此时才可复用slot generation。

Completion与取消必须竞争同一terminal ownership；已经完成的syscall副作用不能被“取消成功”追溯撤销。Go语言没有安全的任意goroutine kill语义：对running G的取消只发布请求，在compiler验证的safepoint或park boundary观察；标准库operation通过`context`、deadline、close或返回error传递取消。只有明确的runtime shutdown/Goexit策略才能终止任务，且必须遵守defer/panic展开语义，不能直接丢弃continuation frame。

## 5. Event source 与 executor contract

### 5.1 Event source

Event source概念上提供以下 owner-side能力；实现不要求使用 Go interface，可由静态 source table、generated ops或目标特化函数实现：

- `Drain(now, completionSink)`：消费已发布事实并提交 runnable completion；
- `NextDeadline()`：返回最早绝对 monotonic deadline；
- `Cancel(OpID)`：竞争或发布取消；
- `Detach/Quiesce/Recycle(OpID)`：分离 waiter与物理source生命周期；
- `Pending()`、`Empty()`：idle和shutdown验证；
- `BeginClose/ConfirmClosed()`：阻止新producer并strong join。

Source-specific submit保留在各自模块，但成功后必须返回统一 `OpID`并遵循上述生命周期。

### 5.2 SourceSet

每个 P/executor绑定一个冻结的 `SourceSet`。Executor不得在主状态机中写 `if timers != nil`、`drain waits then timers` 之类source分支。公共 scan结果只包含：

- completion数量；
- 是否产生 runnable G；
- 最早 deadline；
- pending/requested/invalid状态。

引入第三种 fake source时，compiler不变，executor idle/shutdown算法不复制，只增加source实现和SourceSet注册。这是第一项结构验收。

### 5.3 防丢唤醒 idle transaction

所有平台执行相同协议：

1. Drain完整 SourceSet。
2. 检查 local ready、global injection和preempt request。
3. 发布 `IdleArmed`。
4. 无条件再次 Drain完整 SourceSet。
5. acknowledgement后再无条件重扫一次，覆盖publish与ack之间的producer。
6. 若仍无工作，按最早deadline执行 `CommitSleep`。
7. Platform wait返回后先离开idle gate，再Drain完整 SourceSet。

Doorbell是通知，不是事实源；即使通知被coalesce或出现spurious wake，事实仍在source table/completion queue中。

## 6. 并行模型

### 6.1 逻辑映射

- `G` 是可调度任务。
- `P` 拥有 runnable queue、source shard、timer shard和调度预算。
- `M` 是实际执行上下文，例如native线程、RTOS task、WASM host re-entry或baremetal core loop。
- M必须取得P后才能运行managed G；一次只有一个M拥有某个P。

Runnable G可在P间steal或通过global injection迁移；Running G不可迁移。等待operation记录目标P或可重定向的owner generation。Pinned/ThreadAffine G使用固定M/P协议，不能退化成全局TLS猜测。

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

- Compiler在所有可能无界的 managed path插入suspendable poll。
- Runtime维护独立 `preemptRequested` generation/bitset。
- Native sysmon/tick、WASM slice budget、RTOS tick和baremetal IRQ只负责提出请求与唤醒executor。
- Timer deadline只是一个event deadline；即使没有active timer，CPU-heavy G也必须有界让出。
- `nopreempt`区域必须短、可验证，并在退出时立即处理pending request。
- Post-optimization verifier或等价证明必须保证循环backedge和超长path仍有poll。

## 8. 文件、网络和 timer 如何映射

### 8.1 Timer

公共timer core维护scheduler-owned heap/shard、generation和Go Timer状态。平台只实现monotonic clock、arm earliest deadline和executor wake。`Sleep`、Timer、Ticker、AfterFunc和deadline复用同一source。

### 8.2 网络和可poll fd

`internal/poll`向netpoll source提交fd、interest、deadline和result record；当前G park。Readiness到达后由owner按Go wrapper契约执行一次或重试operation。Raw syscall本身不能擅自改变EINTR、short result或EAGAIN语义。

### 8.3 普通文件和不可poll operation

POSIX regular file、DNS或阻塞C调用根据target capability选择：

- io_uring/IOCP等completion backend；
- 有界blocking worker pool，operation record在worker期间保根/pin；
- thread-affine专用M；
- 单线程host的async import；
- 不支持目标上的明确capability诊断。

不允许为每个operation创建一个G专属pthread或保留调用者native stack。

## 9. 当前实现审查

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
- runtime已有统一逻辑WaitToken/ticket状态机、G wait queue、executor request gate和doorbell防丢唤醒基础。
- 每个函数只选择一个primary body；普通静态路径不生成完整双版本。

尚未达到核心完成条件的部分：

- descriptor codegen当前只有受限plain V1；async function value、interface invoke、capture、method、multi-target和reflect尚未完成。
- package Summary明确不是producer archive ABI；独立预编译标准库的effect传播尚无最终contract。
- Physical coroutine lowering仍是pure-SSA子集，method、closure、generic instance、variadic、recursive/defer/recover和大量runtime helper路径仍fail closed。
- suspended frame没有精确GC root map和write barrier contract。
- Timer frame retention按两个timer符号和精确SSA形状硬编码，证明通用lifetime core缺失。
- ExecutorDriver直接持有wait/timer表并复制timer-aware `*At` 状态机；第三种source会继续扩大driver。
- 抢占预算目前与active timer绑定，不是独立的完整抢占服务。
- 当前driver固定一个P，尚未实现native多P/M、global injection和work stealing。

因此Phase 22应视为首个可运行vertical slice，而不是“核心已经完成后新增一个timer功能”。

## 10. 实现优先级与验收门槛

### P0：统一runtime core

1. 抽取 `SourceSet` 和统一scan/idle/shutdown transaction。
2. 将当前等待cell放入稳定G或operation record；定义scalar `OpID`。
3. 拆分 `DetachWaiter` 与 `RecycleSourceSlot`。
4. 实现G-owned `WaitSet`、`Won/Lost/Invalid` claim和loser cancel/detach barrier。
5. 实现分层执行取消：request、logical terminal、detach和quiesce。
6. 用第三种fake/manual source验证executor不再按source分支。
7. 将抢占请求与timer解耦，并固定P/M/global injection ownership。

### P1：把timer迁入公共模型

1. Timer table改为公共source contract，先保持固定容量保证迁移正确。
2. 再升级dynamic/sharded heap和Go Timer/Stop/Reset/Ticker/AfterFunc语义。
3. Native、WASM/WASI、RTOS和baremetal只实现各自clock/alarm/wait adapter。
4. 删除compiler中的timer symbol-specific frame retention。

### P2：补齐compiler core

1. 通用suspend-region/operation-record lowering和GC frame metadata。
2. `RuntimeCapabilityCatalog`集中管理target capability、runtime roots、ABI signatures和contract IDs。
3. `PackageCoroSummary`作为真实archive ABI。
4. 完成coro descriptor、dynamic child await、method/interface/closure/generic/defer/panic等语言语义。
5. 完成source-independent bounded preemption proof。

### P3：统一模型上的I/O

1. netpoll/readiness source。
2. completion/worker ForeignOp source。
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
