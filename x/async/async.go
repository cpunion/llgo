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
	"unsafe"
	_ "unsafe"
)

const (
	LLGoPackage = "decl"
)

type Void = [0]byte

// -----------------------------------------------------------------------------

type Promise[TOut any] struct {
	hdl   unsafe.Pointer
	e     *executor
	value TOut
}

// coAsync instruction injects a valid Promise object and then calls promise.asyncRun(...).
// llgo:link (*Promise).Async llgo.coAsync
func (p *Promise[TOut]) Async(fn func(resolve func(TOut))) *Promise[TOut] {
	panic("should not executed")
}

// call by llgo.coAsync
func (p *Promise[TOut]) asyncRun(fn func(resolve func(TOut))) *Promise[TOut] {
	a := p.e.loop.NewAsync(func() {
		p.Resume()
	})
	fn(func(v TOut) {
		p.setValue(v)
		err := a.Send()
		if err != nil {
			panic(err)
		}
	})
	p.Suspend()
	return p
}

// // llgo:link (*Promise).Await llgo.coAwait
func (p *Promise[TOut]) Await() TOut {
	panic("should not executed")
}

func (p *Promise[TOut]) Return(v TOut) {
	p.value = v
	coReturn(p.hdl)
}

// llgo:link (*Promise).Yield llgo.coYield
func (p *Promise[TOut]) Yield(v TOut) {}

// call by llgo.coYield
func (p *Promise[TOut]) setValue(v TOut) {
	p.value = v
}

// llgo:link (*Promise).Suspend llgo.coSuspend
func (p *Promise[TOut]) Suspend() {}

func (p *Promise[TOut]) Resume() {
	coResume(p.hdl)
}

func (p *Promise[TOut]) Next() {
	coResume(p.hdl)
}

func (p *Promise[TOut]) Value() TOut {
	return p.value
}

func (p *Promise[TOut]) Done() bool {
	return coDone(p.hdl) != 0
}

func Run(fn func()) {
	e := NewExecutor()
	e.Run(fn)
}
