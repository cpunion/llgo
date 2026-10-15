//go:build !llgo

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
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/packages"
)

func TestLLGoFileContentInvalidatesPackageAndDependentCache(t *testing.T) {
	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	newPackage := func(name, files string) *packages.Package {
		t.Helper()
		dir := filepath.Join(root, name)
		goFile := filepath.Join(dir, "source.go")
		write(goFile, "package p\n")
		p := &packages.Package{
			ID: "example.com/" + name, PkgPath: "example.com/" + name,
			GoFiles: []string{goFile}, Types: types.NewPackage("example.com/"+name, "p"),
		}
		if files != "" {
			p.Types.Scope().Insert(types.NewConst(token.NoPos, p.Types, "LLGoFiles", types.Typ[types.UntypedString], constant.MakeString(files)))
		}
		return p
	}
	// This prefix must be parsed without running pkg-config during collection.
	original := newPackage("original", "$(pkg-config --cflags unavailable): _wrap/leaf.c; ../shared/leaf.S")
	alternate := newPackage("alternate", "_worker/worker.c")
	consumer := newPackage("consumer", "")
	consumer.Imports = map[string]*packages.Package{original.PkgPath: original}
	selected := []string{
		filepath.Join(root, "original", "_wrap", "leaf.c"),
		filepath.Join(root, "shared", "leaf.S"),
		filepath.Join(root, "alternate", "_worker", "worker.c"),
	}
	for _, path := range selected {
		write(path, "value 1\n")
	}
	unselected := filepath.Join(root, "original", "_wrap", "other-target.c")
	write(unselected, "value 1\n")
	fingerprint := func() (string, string) {
		t.Helper()
		producer := &aPackage{Package: original, AltPkg: &packages.Cached{Package: alternate}}
		dependent := &aPackage{Package: consumer}
		ctx := &context{
			buildConf: &Config{}, llvmVersion: "LLVM cache fixture",
			pkgByID: map[string]Package{original.ID: producer},
		}
		if err := ctx.collectFingerprint(dependent); err != nil {
			t.Fatal(err)
		}
		data, err := decodeManifest(producer.Manifest)
		if err != nil {
			t.Fatal(err)
		}
		if len(data.Package.LLGoFiles) != len(selected) {
			t.Fatalf("LLGoFiles = %v, want all three selected source inputs", data.Package.LLGoFiles)
		}
		return producer.Fingerprint, dependent.Fingerprint
	}
	beforeProducer, beforeConsumer := fingerprint()
	for _, path := range selected {
		t.Run(filepath.Base(path), func(t *testing.T) {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			write(path, "value 2\n")
			// Reproduce a source replacement preserving size and timestamps.
			if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
			afterProducer, afterConsumer := fingerprint()
			if afterProducer == beforeProducer || afterConsumer == beforeConsumer {
				t.Fatal("native-only source change did not invalidate producer and consumer fingerprints")
			}
			write(path, "value 1\n")
		})
	}
	write(unselected, "value 2\n")
	afterProducer, afterConsumer := fingerprint()
	if afterProducer != beforeProducer || afterConsumer != beforeConsumer {
		t.Fatal("unselected native source affected target package fingerprints")
	}
	if err := os.Remove(selected[0]); err != nil {
		t.Fatal(err)
	}
	err := collectLLGoFileInputs(newManifestBuilder(), original)
	if err == nil || !strings.Contains(err.Error(), selected[0]) {
		t.Fatalf("missing selected input error = %v", err)
	}
}
