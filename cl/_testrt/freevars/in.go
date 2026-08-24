package main

func main() {
	func(resolve func(error)) {

		func(err error) {
			if err != nil {
				resolve(err)
				return
			}
			resolve(nil)
		}(nil)
	}(func(err error) {
	})
}

// DARWIN-ARM64-NEXT:   call void %__llgo_funcval_code(ptr swiftself %[[TMP5]], %"{{.*}}/runtime/internal/runtime.iface" zeroinitializer)
// LINUX-AMD64-NEXT:   call void %__llgo_funcval_code(ptr nest %[[TMP5]], %"{{.*}}/runtime/internal/runtime.iface" zeroinitializer)

// DARWIN-ARM64-SAME: ptr swiftself %[[TMP0:[0-9]+]], %"{{.*}}/runtime/internal/runtime.iface" %[[TMP1:[0-9]+]]){{.*}} {
// LINUX-AMD64-SAME: ptr nest %[[TMP0:[0-9]+]], %"{{.*}}/runtime/internal/runtime.iface" %[[TMP1:[0-9]+]]){{.*}} {
// DARWIN-ARM64-NEXT:   call void %__llgo_funcval_code(ptr swiftself %[[TMP14]], %"{{.*}}/runtime/internal/runtime.iface" %[[TMP1]])
// LINUX-AMD64-NEXT:   call void %__llgo_funcval_code(ptr nest %[[TMP14]], %"{{.*}}/runtime/internal/runtime.iface" %[[TMP1]])
// DARWIN-ARM64-NEXT:   call void %__llgo_funcval_code1(ptr swiftself %[[TMP18]], %"{{.*}}/runtime/internal/runtime.iface" zeroinitializer)
// LINUX-AMD64-NEXT:   call void %__llgo_funcval_code1(ptr nest %[[TMP18]], %"{{.*}}/runtime/internal/runtime.iface" zeroinitializer)
