/*
 * Copyright (c) 2024 The GoPlus Authors (goplus.org). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package async

import (
	"fmt"
	"unsafe"
	_ "unsafe"

	"github.com/goplus/llgo/c"
	"github.com/goplus/llgo/c/libuv"
)

const (
	LLGoPackage = true
)

type Void = [0]byte

// -----------------------------------------------------------------------------

type Promise[TOut any] struct {
	hdl   unsafe.Pointer
	e     *executor
	value TOut
}

// coAsync instruction injects a valid Promise object and then calls promise.async(...).
// llgo:link Async llgo.coAsync
func Async[TOut any](p *Promise[TOut], fn func(resolve func(TOut))) *Promise[TOut] {
	panic("should not executed")
}

// call by llgo.coAsync
func (p *Promise[TOut]) async(fn func(resolve func(TOut))) *Promise[TOut] {
	println("=== async")
	p.e = Executor()
	fmt.Printf("fn: %T\n", fn)
	a := &async{cb: func() {
		println("before resume in callback")
		p.e.Resume()
		println("after Resume in callback")
	}}
	println("before register Async")
	r := p.e.loop.Async(&a.Async, func(pa *libuv.Async) {
		a := (*async)(unsafe.Pointer(pa))
		a.Close(nil)
		a.cb()
	})
	println("after register Async")
	println("r:", r)
	println("r != 0", r != 0)
	if r != 0 {
		println("Async failed")
		panic("Async failed")
	}
	println("before fn")
	println("before fn p", p.Done())
	fn(func(v TOut) {
		p.setValue(v)
		if r := a.Send(); r != 0 {
			panic("Send failed")
		}
	})
	println("after fn")
	println("before return p", p.Done())
	return p
}

// llgo:link Await llgo.coAwait
func Await[TOut any](p *Promise[TOut]) TOut {
	panic("should not executed")
}

// llgo:link Suspend llgo.coSuspend
func Suspend[TOut any](p *Promise[TOut]) {}

// call by llgo.coAwait
// p.Await() in f() will be translated to:
//
//	 f.promise.suspend()
//		p.afterAwait()
func (p *Promise[TOut]) afterAwait() TOut {
	println("afterAwait", p)
	if p.Done() {
		return p.value
	}
	coResume(p.hdl)
	println("afterAwait done", p)
	return p.value
}

func (p *Promise[TOut]) Return(v TOut) {
	p.value = v
}

// llgo:link Yield llgo.coYield
func Yield[TOut any](p *Promise[TOut], v TOut) {}

// call by llgo.coYield
func (p *Promise[TOut]) setValue(v TOut) {
	p.value = v
}

// llgo:link coResume llgo.coResume
func coResume(hdl unsafe.Pointer) {
	panic("should not executed")
}

func (p *Promise[TOut]) Resume() {
	println("before coResume", p, "hdl:", p.hdl)
	coResume(p.hdl)
	println("after coResume")
}

func (p *Promise[TOut]) Next() {
	coResume(p.hdl)
}

func (p *Promise[TOut]) Value() TOut {
	return p.value
}

// llgo:link coDone llgo.coDone
func coDone(hdl unsafe.Pointer) c.Char {
	panic("should not executed")
}

func (p *Promise[TOut]) Done() bool {
	return coDone(p.hdl) != 0
}

type promise interface {
	Resume()
	Done() bool
}

func Run[TOut any](fn func() *Promise[TOut]) TOut {
	var value TOut
	e := NewExecutor()
	e.Run(func() promise {
		p := fn()
		fmt.Printf("run promise: %T\n", p)
		println("ptr:", p)
		println("done:", p.Done())
		p.Resume()
		var r promise = p
		println("run promise done")
		return r
	})
	return value
}

// -----------------------------------------------------------------------------

type async struct {
	libuv.Async
	cb func()
}
