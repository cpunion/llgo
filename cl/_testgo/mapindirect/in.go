// LITTEST
package main

// LargeKey is larger than the runtime's inline map key limit. Maps store
// these keys indirectly, so this also exercises the generic map ABI path.
type LargeKey [256]byte

func lookup[K comparable](m map[K]int, key K) (int, bool) {
	value, ok := m[key]
	return value, ok
}

func main() {
	m := make(map[LargeKey]int)
	for i := 0; i < 32; i++ {
		var key LargeKey
		key[0] = byte(i)
		m[key] = i
	}

	var target LargeKey
	target[0] = 17
	value, ok := lookup(m, target)

	sum := 0
	for key, value := range m {
		sum += int(key[0]) + value
	}
	println(value, ok, len(m), sum)
}

// The stackless form must retain the indirect-key allocation while routing
// every potentially suspending map operation through its typed coroutine
// entry. Runtime output verifies the complete loop and lookup semantics.
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 256)
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapassign$coro"(
// CHECK-DAG: call ptr @"main.lookup[main.LargeKey]$coro"(
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.NewMapIter$coro"(
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.MapIterNext$coro"(

// CHECK-LABEL: define linkonce ptr @"main.lookup[main.LargeKey]$coro"(
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 256)
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapaccess2$coro"(
