// LITTEST
package main

import "sync"

// Keep this check structural. Coroutine lowering deliberately changes the
// instruction sequence as scheduler protocol details evolve.
// CHECK: {{^}}@{{[0-9]+}} = private unnamed_addr constant [7 x i8] c"Do once", align 1{{$}}
// CHECK: {{^}}@{{[0-9]+}} = private unnamed_addr constant [8 x i8] c"Do twice", align 1{{$}}

var once sync.Once

func f(s string) {
	once.Do(func() {
		println(s)
	})
}

func main() {
	f("Do once")
	f("Do twice")
}

// Calling sync.Once.Do is a suspension seed, so the complete caller chain is
// emitted with the coroutine ABI. The closure retains its explicit environment
// as the third physical parameter.
// CHECK-LABEL: define ptr @"main.f$coro"(
// CHECK: call ptr @"sync.(*Once).Do$coro"(
// CHECK: call void @__llgo_coro_await_prepare_v3(
// CHECK: call i8 @llvm.coro.suspend(

// CHECK-LABEL: define ptr @"main.f$1$coro"(ptr %0, ptr %1, ptr swiftself %2)
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintString$coro"(

// Package initialization is part of the same managed startup chain.
// CHECK-LABEL: define ptr @"main.init$coro"(
// CHECK: call ptr @"sync.init$coro"(

// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call ptr @"main.f$coro"(
// CHECK: call ptr @"main.f$coro"(
