; ModuleID = 'github.com/goplus/llgo/compiler/cl/_testlibgo/mathbits'
source_filename = "github.com/goplus/llgo/compiler/cl/_testlibgo/mathbits"

@"github.com/goplus/llgo/compiler/cl/_testlibgo/mathbits.init$guard" = global i1 false, align 1

define void @"github.com/goplus/llgo/compiler/cl/_testlibgo/mathbits.init"() {
_llgo_0:
  %0 = load i1, ptr @"github.com/goplus/llgo/compiler/cl/_testlibgo/mathbits.init$guard", align 1
  br i1 %0, label %_llgo_2, label %_llgo_1

_llgo_1:                                          ; preds = %_llgo_0
  store i1 true, ptr @"github.com/goplus/llgo/compiler/cl/_testlibgo/mathbits.init$guard", align 1
  call void @"math/bits.init"()
  br label %_llgo_2

_llgo_2:                                          ; preds = %_llgo_1, %_llgo_0
  ret void
}

define void @"github.com/goplus/llgo/compiler/cl/_testlibgo/mathbits.main"() {
_llgo_0:
  %0 = call i64 @"math/bits.Len8"(i8 20)
  call void @"github.com/goplus/llgo/runtime/internal/runtime.PrintInt"(i64 %0)
  call void @"github.com/goplus/llgo/runtime/internal/runtime.PrintByte"(i8 10)
  %1 = call i64 @"math/bits.OnesCount"(i64 20)
  call void @"github.com/goplus/llgo/runtime/internal/runtime.PrintInt"(i64 %1)
  call void @"github.com/goplus/llgo/runtime/internal/runtime.PrintByte"(i8 10)
  ret void
}

declare void @"math/bits.init"()

declare i64 @"math/bits.Len8"(i8)

declare void @"github.com/goplus/llgo/runtime/internal/runtime.PrintInt"(i64)

declare void @"github.com/goplus/llgo/runtime/internal/runtime.PrintByte"(i8)

declare i64 @"math/bits.OnesCount"(i64)
