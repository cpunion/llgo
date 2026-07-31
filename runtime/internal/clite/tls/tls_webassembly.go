//go:build llgo && (wasm || tinygo.wasm) && !baremetal

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
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

package tls

// Logical WebAssembly targets currently expose one serialized host activation
// domain. A Handle therefore needs one program-lifetime cell, not a POSIX TLS
// key. Keeping the cell behind a pointer preserves Handle copy semantics.
//
// WebAssembly threads require a distinct target capability and implementation;
// they must not silently select this profile or import pthread symbols.
type Handle[T any] struct {
	cell *T
}

type StaticHandle[T any] struct {
	cell *T
}

func Alloc[T any](func(*T)) Handle[T] {
	return Handle[T]{cell: new(T)}
}

func AllocStatic[T any]() StaticHandle[T] {
	return StaticHandle[T]{cell: new(T)}
}

func (h Handle[T]) Get() (zero T) {
	if h.cell == nil {
		return zero
	}
	return *h.cell
}

func (h Handle[T]) Set(value T) {
	if h.cell != nil {
		*h.cell = value
	}
}

func (h Handle[T]) Clear() {
	if h.cell != nil {
		var zero T
		*h.cell = zero
	}
}

func (h StaticHandle[T]) Get() (zero T) {
	if h.cell == nil {
		return zero
	}
	return *h.cell
}

func (h StaticHandle[T]) Set(value T) {
	if h.cell != nil {
		*h.cell = value
	}
}

func (h StaticHandle[T]) Clear() {
	if h.cell != nil {
		var zero T
		*h.cell = zero
	}
}
