// LITTEST
package main

import (
	"github.com/goplus/lib/c"
	"github.com/goplus/lib/cpp/llvm"
)

// CHECK: @0 = private unnamed_addr constant [29 x i8] c"__ZNK9INIReader10ParseErrorEv", align 1
// CHECK-LABEL: define ptr @"{{.*}}/cl/_testlibc/demangle.main$coro"(
func main() {
	mangledName := "__ZNK9INIReader10ParseErrorEv"
	// CHECK: store %"{{.*}}/runtime/internal/runtime.String" { ptr @0, i64 29 }
	// CHECK: call void @__llgo_coro_worker_park_v1
	if name := llvm.ItaniumDemangle(mangledName, true); name != nil {
		c.Printf(c.Str("%s\n"), name)
	} else {
		println("Failed to demangle")
	}
}

// CHECK-LABEL: define linkonce i64 @__llgo_coro_worker_foreign_thunk_v1_
// CHECK: call ptr @_ZN4llvm15itaniumDemangleENSt3__117basic_string_viewIcNS0_11char_traitsIcEEEEb(%"{{.*}}/runtime/internal/runtime.String" {{%.*}}, i1 {{%.*}})
// CHECK-LABEL: define linkonce i64 @__llgo_coro_worker_foreign_thunk_v1_
// CHECK: call i32 (ptr, ...) @printf(ptr {{%.*}}, ptr {{%.*}})
