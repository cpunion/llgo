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

// Package wasmresume defines the runtime half of LLGo's experimental
// WebAssembly resumable call ABI.
package wasmresume

// Action tells Context what a resume entry did.
type Action uint8

const (
	// Continue means that execution can continue immediately. The resume entry
	// may have pushed a child frame or advanced within the current frame.
	Continue Action = iota

	// Return means that the current frame completed normally.
	Return

	// Suspend returns control to the scheduler without changing the frame chain.
	Suspend
)

// Resume is the non-suspending indirect-call signature for generated entries.
//
//llgo:type C
type Resume func(*Context, *Frame) Action

// Descriptor contains immutable state shared by every invocation of a
// generated function.
type Descriptor struct {
	Resume     Resume
	FrameSize  uintptr
	FrameAlign uintptr
}

// Frame is the common prefix of every generated function frame. Generated
// frame types must embed Frame as their first field.
type Frame struct {
	Parent     *Frame
	Descriptor *Descriptor
	PC         uint32
}

// Context owns the active frame chain for one logical goroutine.
type Context struct {
	top      *Frame
	returned *Frame
}

// Top returns the active frame.
func (c *Context) Top() *Frame {
	return c.top
}

// Push links frame as the active child of the current frame.
func (c *Context) Push(frame *Frame, descriptor *Descriptor) {
	if c.top == nil {
		c.returned = nil
	}
	frame.Parent = c.top
	frame.Descriptor = descriptor
	frame.PC = 0
	c.top = frame
}

// TakeReturned returns the child frame that completed immediately before the
// active frame resumed. It also transfers ownership back to the caller.
func (c *Context) TakeReturned() *Frame {
	frame := c.returned
	c.returned = nil
	return frame
}

// Run resumes the active frame chain until it completes or suspends.
func (c *Context) Run() Action {
	for c.top != nil {
		frame := c.top
		switch frame.Descriptor.Resume(c, frame) {
		case Continue:
		case Return:
			c.top = frame.Parent
			c.returned = frame
		case Suspend:
			return Suspend
		default:
			panic("wasmresume: invalid resume action")
		}
	}
	return Return
}
