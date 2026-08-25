// LITTEST
package main

import "unsafe"

type named32 uint32
type named64 uint64
type namedString string

type structKey struct {
	value uint64
}

var sink int

// CHECK-LABEL: define ptr @"main.testChannel$coro"(
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapassign_fast64ptr$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapaccess1_fast64$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapaccess2_fast64$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapdelete_fast64$coro"
// CHECK-LABEL: define ptr @"main.testFallback$coro"(
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapassign$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapaccess1$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapaccess2$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapdelete$coro"
// CHECK-LABEL: define ptr @"main.testInt$coro"(
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapassign_fast64$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapaccess1_fast64$coro"
// CHECK-LABEL: define ptr @"main.testPointer$coro"(
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapassign_fast64ptr$coro"
// CHECK-DAG: ptrtoint ptr %2 to i64
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapaccess1_fast64$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapaccess2_fast64$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapdelete_fast64$coro"
// CHECK-LABEL: define ptr @"main.testString$coro"(
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapassign_faststr$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapaccess1_faststr$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapaccess2_faststr$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapdelete_faststr$coro"
// CHECK-LABEL: define ptr @"main.testStringFunc$coro"(
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.mapassign_faststr$coro"
// CHECK-LABEL: define ptr @"main.testUint32$coro"(
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapassign_fast32$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapaccess1_fast32$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapaccess2_fast32$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapdelete_fast32$coro"
// CHECK-LABEL: define ptr @"main.testUint64$coro"(
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapassign_fast64$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapaccess1_fast64$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapaccess2_fast64$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapdelete_fast64$coro"
// CHECK-LABEL: define ptr @"main.testUnsafePointer$coro"(
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapassign_fast64ptr$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapaccess1_fast64$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapaccess2_fast64$coro"
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.mapdelete_fast64$coro"

func testUint32() {
	m := make(map[named32]int)
	m[7] = 11
	sink += m[7]
	if value, ok := m[7]; ok {
		sink += value
	}
	delete(m, 7)
}

func testUint64() {
	m := make(map[named64]int)
	m[1<<40] = 13
	sink += m[1<<40]
	if value, ok := m[1<<40]; ok {
		sink += value
	}
	delete(m, 1<<40)
}

func testInt() {
	m := make(map[int]int)
	m[37] = 37
	sink += m[37]
}

func testString() {
	m := make(map[namedString]int)
	m["fast"] = 17
	sink += m["fast"]
	if value, ok := m["fast"]; ok {
		sink += value
	}
	delete(m, "fast")
}

func addSink() {
	sink++
}

func testStringFunc() {
	m := make(map[string]func())
	m["call"] = addSink
	m["call"]()
}

func testPointer(key *int) {
	m := make(map[*int]int)
	m[key] = 19
	sink += m[key]
	if value, ok := m[key]; ok {
		sink += value
	}
	delete(m, key)
}

func testChannel(key chan int) {
	m := make(map[chan int]int)
	m[key] = 23
	sink += m[key]
	if value, ok := m[key]; ok {
		sink += value
	}
	delete(m, key)
}

func testUnsafePointer(key unsafe.Pointer) {
	m := make(map[unsafe.Pointer]int)
	m[key] = 29
	sink += m[key]
	if value, ok := m[key]; ok {
		sink += value
	}
	delete(m, key)
}

func testFallback() {
	key := structKey{31}
	m := make(map[structKey]int)
	m[key] = 31
	sink += m[key]
	if value, ok := m[key]; ok {
		sink += value
	}
	delete(m, key)
}

func main() {
	value := 1
	testUint32()
	testUint64()
	testInt()
	testString()
	testStringFunc()
	testPointer(&value)
	testChannel(make(chan int))
	testUnsafePointer(unsafe.Pointer(&value))
	testFallback()
	println(sink)
}
