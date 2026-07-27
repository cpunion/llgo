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

package build

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/xtool/ar"
)

const maxCoroLibraryEffectArchiveBytes = 256 << 20

// ReadCoroLibraryEffectArchive reads the format-neutral producer records from
// one regular ar archive. It decodes only the reserved minimal sidecar object
// and never opens package native code or LLVM bitcode. found=false means the
// archive carries no LLGo coroutine producer contract; callers must not
// reinterpret that absence as NoSuspend.
func ReadCoroLibraryEffectArchive(path string) (
	summaries []coro.LibraryEffectSummary,
	found bool,
	err error,
) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("open coroutine library archive %q: %w", path, err)
	}
	defer file.Close()
	reader, err := ar.NewReader(file)
	if err != nil {
		return nil, false, fmt.Errorf("read coroutine library archive %q: %w", path, err)
	}
	var records []byte
	members := 0
	for {
		header, nextErr := reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return nil, false, fmt.Errorf("scan coroutine library archive %q: %w", path, nextErr)
		}
		name := strings.TrimRight(header.Name, "/ \x00")
		if name != coro.LibraryEffectArchiveMember {
			continue
		}
		found = true
		members++
		if members != 1 {
			return nil, true, fmt.Errorf(
				"coroutine library archive %q contains duplicate member %q",
				path, coro.LibraryEffectArchiveMember,
			)
		}
		if header.Size < 0 || header.Size > int64(coro.LibraryEffectArchiveMemberMaxBytes) {
			return nil, true, fmt.Errorf(
				"coroutine library archive %q member %q has invalid size %d",
				path, header.Name, header.Size,
			)
		}
		if int64(len(records))+header.Size > maxCoroLibraryEffectArchiveBytes {
			return nil, true, fmt.Errorf(
				"coroutine library archive %q metadata exceeds %d bytes",
				path, maxCoroLibraryEffectArchiveBytes,
			)
		}
		member := make([]byte, int(header.Size))
		if _, readErr := io.ReadFull(reader, member); readErr != nil {
			return nil, true, fmt.Errorf(
				"read coroutine library archive %q member %q: %w",
				path, header.Name, readErr,
			)
		}
		// Darwin's BSD ar rounds extended-name member payloads to an
		// eight-byte boundary and includes newline padding in Header.Size.
		// Strip only that bounded transport padding; the framed record and its
		// digest remain exact, and every other trailing byte still fails closed.
		padding := len(member) - len(bytes.TrimRight(member, "\n"))
		if padding > 7 {
			return nil, true, fmt.Errorf(
				"coroutine library archive %q member %q has invalid transport padding",
				path, header.Name,
			)
		}
		if padding > 0 {
			member = member[:len(member)-padding]
		}
		memberRecords, readErr := readCoroLibraryEffectObject(member)
		if readErr != nil {
			return nil, true, fmt.Errorf(
				"decode coroutine library archive %q member %q: %w",
				path, header.Name, readErr,
			)
		}
		records = append(records, memberRecords...)
	}
	if !found {
		return nil, false, nil
	}
	summaries, err = coro.ParseLibraryEffectSummaryRecords(records)
	if err != nil {
		return nil, true, fmt.Errorf("parse coroutine library archive %q: %w", path, err)
	}
	if len(summaries) != 1 {
		return nil, true, fmt.Errorf(
			"coroutine library archive %q contains %d producer summaries, want 1",
			path, len(summaries),
		)
	}
	return summaries, true, nil
}
