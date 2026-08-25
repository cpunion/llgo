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
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/xgo-dev/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

const coroImportedProgramCapabilitiesDigestDomain = "llgo.coro.imported-program-capabilities.v1"

// coroImportedProgramCapabilitiesDigest binds the only imported producer fact
// which changes optional runtime-service emission without changing the
// function's structural ABI or suspend effect. Zero-capability facts need no
// entry: adding or removing one cannot alter generated code.
func coroImportedProgramCapabilitiesDigest(
	effects map[*ssa.Function]coro.LibraryEffectFunction,
) (string, error) {
	type record struct {
		id           coro.FunctionID
		capabilities coro.ProgramCapabilities
	}
	records := make([]record, 0, len(effects))
	seen := make(map[coro.FunctionID]struct{}, len(effects))
	for function, fact := range effects {
		if function == nil || fact.ID == "" {
			return "", fmt.Errorf("imported program capability has no exact function identity")
		}
		if !fact.ProgramCapabilities.Valid() {
			return "", fmt.Errorf("imported function %q has invalid program capabilities %#x", fact.ID, fact.ProgramCapabilities)
		}
		if _, duplicate := seen[fact.ID]; duplicate {
			return "", fmt.Errorf("duplicate imported program capability for %q", fact.ID)
		}
		seen[fact.ID] = struct{}{}
		if fact.ProgramCapabilities == 0 {
			continue
		}
		records = append(records, record{id: fact.ID, capabilities: fact.ProgramCapabilities})
	}
	if len(records) == 0 {
		return "", nil
	}
	sort.Slice(records, func(i, j int) bool { return records[i].id < records[j].id })
	hash := sha256.New()
	_, _ = hash.Write([]byte(coroImportedProgramCapabilitiesDigestDomain))
	var size [8]byte
	for _, item := range records {
		binary.BigEndian.PutUint64(size[:], uint64(len(item.id)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(item.id))
		_, _ = hash.Write([]byte{byte(item.capabilities)})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
