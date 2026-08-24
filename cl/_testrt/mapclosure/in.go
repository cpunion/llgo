package main

type Type interface {
	String() string
}

func demo(t Type) string {
	return t.String()
}

type typ struct {
	s string
}

var (
	op = map[string]func(Type) string{
		"demo": demo,
	}
	list = []func(Type) string{demo}
)

// DARWIN-ARM64: [[MAP_RESULT:%[0-9]+]] = call %"{{.*}}/runtime/internal/runtime.String" %__llgo_funcval_code(ptr swiftself [[MAP_ENV]], %"{{.*}}/runtime/internal/runtime.iface" [[MAP_ARG:%[0-9]+]])
// LINUX-AMD64: [[MAP_RESULT:%[0-9]+]] = call %"{{.*}}/runtime/internal/runtime.String" %__llgo_funcval_code(ptr nest [[MAP_ENV]], %"{{.*}}/runtime/internal/runtime.iface" [[MAP_ARG:%[0-9]+]])
// DARWIN-ARM64: [[LIST_RESULT:%[0-9]+]] = call %"{{.*}}/runtime/internal/runtime.String" %__llgo_funcval_code1(ptr swiftself [[LIST_ENV]], %"{{.*}}/runtime/internal/runtime.iface" [[LIST_ARG:%[0-9]+]])
// LINUX-AMD64: [[LIST_RESULT:%[0-9]+]] = call %"{{.*}}/runtime/internal/runtime.String" %__llgo_funcval_code1(ptr nest [[LIST_ENV]], %"{{.*}}/runtime/internal/runtime.iface" [[LIST_ARG:%[0-9]+]])
func main() {
	t := &typ{"hello"}
	fn1 := op["demo"]
	fn2 := list[0]
	if fn1(t) != fn2(t) {
		panic("error")
	}
}

func (t *typ) String() string {
	return t.s
}
