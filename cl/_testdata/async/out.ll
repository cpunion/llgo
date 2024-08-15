; ModuleID = 'async'
source_filename = "async"

%"github.com/goplus/llgo/x/async.Promise[int]" = type { ptr, ptr, i64 }

@"async.init$guard" = global i1 false, align 1

define ptr @async.GenInts() presplitcoroutine {
_llgo_0:
  %id = call token @llvm.coro.id(i32 0, ptr null, ptr null, ptr null)
  %frame.size = call i64 @llvm.coro.size.i64()
  %alloc.size = add i64 24, %frame.size
  %promise = call ptr @"github.com/goplus/llgo/internal/runtime.AllocZ"(i64 %alloc.size)
  %need.dyn.alloc = call i1 @llvm.coro.alloc(token %id)
  br i1 %need.dyn.alloc, label %alloc, label %begin

alloc:                                            ; preds = %_llgo_0
  %0 = getelementptr ptr, ptr %promise, i64 24
  br label %begin

begin:                                            ; preds = %alloc, %_llgo_0
  %frame = phi ptr [ null, %_llgo_0 ], [ %0, %alloc ]
  %hdl = call ptr @llvm.coro.begin(token %id, ptr %frame)
  store ptr %hdl, ptr %promise, align 8
  call void @"github.com/goplus/llgo/x/async.(*Promise[int]).setValue"(ptr %promise, i64 1)
  %1 = call i8 @llvm.coro.suspend(token %id, i1 false)
  switch i8 %1, label %suspend [
    i8 0, label %resume
    i8 1, label %clean
  ]

clean:                                            ; preds = %resume2, %resume1, %resume, %begin
  %2 = call ptr @llvm.coro.free(token %id, ptr %hdl)
  br label %suspend

suspend:                                          ; preds = %resume2, %resume1, %resume, %begin, %clean
  %3 = call i1 @llvm.coro.end(ptr %hdl, i1 false, token none)
  ret ptr %promise

trap:                                             ; preds = %resume2
  call void @llvm.trap()
  unreachable

resume:                                           ; preds = %begin
  call void @"github.com/goplus/llgo/x/async.(*Promise[int]).setValue"(ptr %promise, i64 2)
  %4 = call i8 @llvm.coro.suspend(token %id, i1 false)
  switch i8 %4, label %suspend [
    i8 0, label %resume1
    i8 1, label %clean
  ]

resume1:                                          ; preds = %resume
  call void @"github.com/goplus/llgo/x/async.(*Promise[int]).setValue"(ptr %promise, i64 3)
  %5 = call i8 @llvm.coro.suspend(token %id, i1 false)
  switch i8 %5, label %suspend [
    i8 0, label %resume2
    i8 1, label %clean
  ]

resume2:                                          ; preds = %resume1
  %6 = call i8 @llvm.coro.suspend(token %id, i1 true)
  switch i8 %6, label %suspend [
    i8 0, label %trap
    i8 1, label %clean
  ]
}

define i64 @async.UseGenInts() {
_llgo_0:
  %0 = call ptr @async.WrapGenInts()
  br label %_llgo_3

_llgo_1:                                          ; preds = %_llgo_3
  %1 = call i64 @"github.com/goplus/llgo/x/async.(*Promise[int]).Value"(ptr %0)
  %2 = add i64 %3, %1
  call void @"github.com/goplus/llgo/x/async.(*Promise[int]).Next"(ptr %0)
  br label %_llgo_3

_llgo_2:                                          ; preds = %_llgo_3
  ret i64 %3

_llgo_3:                                          ; preds = %_llgo_1, %_llgo_0
  %3 = phi i64 [ 0, %_llgo_0 ], [ %2, %_llgo_1 ]
  %4 = call i1 @"github.com/goplus/llgo/x/async.(*Promise[int]).Done"(ptr %0)
  br i1 %4, label %_llgo_2, label %_llgo_1
}

define ptr @async.WrapGenInts() presplitcoroutine {
_llgo_0:
  %id = call token @llvm.coro.id(i32 0, ptr null, ptr null, ptr null)
  %frame.size = call i64 @llvm.coro.size.i64()
  %alloc.size = add i64 24, %frame.size
  %promise = call ptr @"github.com/goplus/llgo/internal/runtime.AllocZ"(i64 %alloc.size)
  %need.dyn.alloc = call i1 @llvm.coro.alloc(token %id)
  br i1 %need.dyn.alloc, label %alloc, label %begin

alloc:                                            ; preds = %_llgo_0
  %0 = getelementptr ptr, ptr %promise, i64 24
  br label %begin

begin:                                            ; preds = %alloc, %_llgo_0
  %frame = phi ptr [ null, %_llgo_0 ], [ %0, %alloc ]
  %hdl = call ptr @llvm.coro.begin(token %id, ptr %frame)
  store ptr %hdl, ptr %promise, align 8
  %1 = call ptr @async.GenInts()
  %2 = call i8 @llvm.coro.suspend(token %id, i1 true)
  switch i8 %2, label %suspend [
    i8 0, label %trap
    i8 1, label %clean
  ]

clean:                                            ; preds = %begin
  %3 = call ptr @llvm.coro.free(token %id, ptr %hdl)
  br label %suspend

suspend:                                          ; preds = %begin, %clean
  %4 = call i1 @llvm.coro.end(ptr %hdl, i1 false, token none)
  ret ptr %promise

trap:                                             ; preds = %begin
  call void @llvm.trap()
  unreachable
}

define void @async.init() {
_llgo_0:
  %0 = load i1, ptr @"async.init$guard", align 1
  br i1 %0, label %_llgo_2, label %_llgo_1

_llgo_1:                                          ; preds = %_llgo_0
  store i1 true, ptr @"async.init$guard", align 1
  call void @"github.com/goplus/llgo/x/async.init"()
  br label %_llgo_2

_llgo_2:                                          ; preds = %_llgo_1, %_llgo_0
  ret void
}

; Function Attrs: nocallback nofree nosync nounwind willreturn memory(argmem: read)
declare token @llvm.coro.id(i32, ptr readnone, ptr nocapture readonly, ptr)

; Function Attrs: nounwind memory(none)
declare i64 @llvm.coro.size.i64()

declare ptr @"github.com/goplus/llgo/internal/runtime.AllocZ"(i64)

; Function Attrs: nounwind
declare i1 @llvm.coro.alloc(token)

; Function Attrs: nounwind
declare ptr @llvm.coro.begin(token, ptr writeonly)

; Function Attrs: nounwind memory(argmem: read)
declare ptr @llvm.coro.free(token, ptr nocapture readonly)

; Function Attrs: nounwind
declare i1 @llvm.coro.end(ptr, i1, token)

; Function Attrs: cold noreturn nounwind memory(inaccessiblemem: write)
declare void @llvm.trap()

define linkonce void @"github.com/goplus/llgo/x/async.(*Promise[int]).setValue"(ptr %0, i64 %1) {
_llgo_0:
  %2 = getelementptr inbounds %"github.com/goplus/llgo/x/async.Promise[int]", ptr %0, i32 0, i32 2
  store i64 %1, ptr %2, align 4
  ret void
}

; Function Attrs: nounwind
declare i8 @llvm.coro.suspend(token, i1)

define linkonce i1 @"github.com/goplus/llgo/x/async.(*Promise[int]).Done"(ptr %0) {
_llgo_0:
  %1 = getelementptr inbounds %"github.com/goplus/llgo/x/async.Promise[int]", ptr %0, i32 0, i32 0
  %2 = load ptr, ptr %1, align 8
  %3 = call i1 @llvm.coro.done(ptr %2)
  %4 = zext i1 %3 to i64
  %5 = trunc i64 %4 to i8
  %6 = icmp ne i8 %5, 0
  ret i1 %6
}

define linkonce i64 @"github.com/goplus/llgo/x/async.(*Promise[int]).Value"(ptr %0) {
_llgo_0:
  %1 = getelementptr inbounds %"github.com/goplus/llgo/x/async.Promise[int]", ptr %0, i32 0, i32 2
  %2 = load i64, ptr %1, align 4
  ret i64 %2
}

define linkonce void @"github.com/goplus/llgo/x/async.(*Promise[int]).Next"(ptr %0) {
_llgo_0:
  %1 = getelementptr inbounds %"github.com/goplus/llgo/x/async.Promise[int]", ptr %0, i32 0, i32 0
  %2 = load ptr, ptr %1, align 8
  call void @llvm.coro.resume(ptr %2)
  ret void
}

declare void @"github.com/goplus/llgo/x/async.init"()

; Function Attrs: nounwind memory(argmem: readwrite)
declare i1 @llvm.coro.done(ptr nocapture readonly)

declare void @llvm.coro.resume(ptr)

