// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type sourcePatchRequirement struct {
	filename string
	digest   [sha256.Size]byte
}

// require-source restricts a replacement to an exact standard-library source
// file. A Go release tag alone cannot authenticate patch-level changes. The
// digest covers the loader's overlay view, including import and type bindings.
func parseSourcePatchRequirement(line string) (sourcePatchRequirement, bool, error) {
	var requirement sourcePatchRequirement
	tail := strings.TrimPrefix(line, "//llgo:")
	if tail == line {
		tail = strings.TrimPrefix(line, "// llgo:")
	}
	fields := strings.Fields(tail)
	if len(fields) == 0 || fields[0] != "require-source" || tail == line {
		return requirement, false, nil
	}
	if len(fields) != 3 || fields[1] == "." || fields[1] == ".." ||
		strings.ContainsAny(fields[1], "/\\:") || !strings.HasSuffix(fields[1], ".go") {
		return requirement, true, fmt.Errorf("invalid require-source directive %q", line)
	}
	digest, err := hex.DecodeString(fields[2])
	if err != nil || len(digest) != sha256.Size {
		return requirement, true, fmt.Errorf("invalid require-source SHA-256 in %q", line)
	}
	requirement.filename = fields[1]
	copy(requirement.digest[:], digest)
	return requirement, true, nil
}

func sourcePatchRequirementsMatch(requirements []sourcePatchRequirement, srcDir string, read func(string) ([]byte, error)) (bool, error) {
	for _, requirement := range requirements {
		src, err := read(filepath.Join(srcDir, requirement.filename))
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if sha256.Sum256(src) != requirement.digest {
			return false, nil
		}
	}
	return true, nil
}
