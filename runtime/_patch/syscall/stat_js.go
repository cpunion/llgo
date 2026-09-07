//go:build llgo && js && wasm

// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license in runtime/LICENSES/Go-BSD-3-Clause.txt.

package syscall

import "syscall/js"

// Adapt only GOROOT's setStat numeric boundary. Official Go wasm uses a 64-bit
// int; LLGo's C32 data model does not. Convert JS numbers directly to the final
// 64-bit fields, without truncating timestamps, file sizes, or inode numbers
// through Value.Int. The surrounding filesystem implementation stays in Go.
func setStat(st *Stat_t, jsSt js.Value) {
	st.Dev = int64(jsSt.Get("dev").Float())
	st.Ino = uint64(jsSt.Get("ino").Float())
	st.Mode = uint32(jsSt.Get("mode").Float())
	st.Nlink = uint32(jsSt.Get("nlink").Float())
	st.Uid = uint32(jsSt.Get("uid").Float())
	st.Gid = uint32(jsSt.Get("gid").Float())
	st.Rdev = int64(jsSt.Get("rdev").Float())
	st.Size = int64(jsSt.Get("size").Float())
	st.Blksize = int32(jsSt.Get("blksize").Int())
	st.Blocks = int32(jsSt.Get("blocks").Int())
	atime := int64(jsSt.Get("atimeMs").Float())
	st.Atime = atime / 1000
	st.AtimeNsec = (atime % 1000) * 1000000
	mtime := int64(jsSt.Get("mtimeMs").Float())
	st.Mtime = mtime / 1000
	st.MtimeNsec = (mtime % 1000) * 1000000
	ctime := int64(jsSt.Get("ctimeMs").Float())
	st.Ctime = ctime / 1000
	st.CtimeNsec = (ctime % 1000) * 1000000
}
