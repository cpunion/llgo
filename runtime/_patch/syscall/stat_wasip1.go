//go:build llgo && wasip1 && wasm

// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license in runtime/LICENSES/Go-BSD-3-Clause.txt.

package syscall

// Keep the selected GOROOT's syscall functions. They pass these records
// directly to WASI; explicit padding preserves the WASI record offsets even
// when LLGo's Go value layout aligns uint64 to four bytes on C32 profiles.
type Stat_t struct {
	Dev      uint64
	Ino      uint64
	Filetype uint8
	_        [7]byte
	Nlink    uint64
	Size     uint64
	Atime    uint64
	Mtime    uint64
	Ctime    uint64
	Mode     int
	Uid      uint32
	Gid      uint32
}

type fdstat struct {
	filetype         filetype
	_                byte
	fdflags          uint16
	_                [4]byte
	rightsBase       rights
	rightsInheriting rights
}
