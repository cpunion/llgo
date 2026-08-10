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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/goplus/llgo/xtool/ar"
	gllvm "github.com/xgo-dev/llvm"
)

type coroArchiveTestMember struct {
	name string
	data []byte
}

func coroArchiveTestSummary(t *testing.T, pkg string) coro.LibraryEffectSummary {
	t.Helper()
	functionID := coro.FunctionID("llgo.function.v0:" + pkg)
	return coro.LibraryEffectSummary{
		Schema:  coro.LibraryEffectSummarySchema,
		Package: pkg,
		Metadata: coro.LibraryEffectMetadata{
			FunctionIDSchema:   coro.FunctionIDSchema,
			CoroABI:            coro.PhysicalABIV1,
			SchedulerABI:       coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0,
			PanicABI:           coro.PanicExplicitStatusABIV0,
			FuncRepABI:         coro.FuncRepABIV1,
			TargetTriple:       "x86_64-unknown-linux-gnu",
			PointerBits:        64,
			Endianness:         "little",
			DataLayout:         "e-m:e-p:64:64",
			TargetCapabilities: coro.NewTargetCapabilities(true, true, false),
		},
		Functions: []coro.LibraryEffectFunction{{
			ID:            functionID,
			ABIHash:       strings.Repeat("a", 64),
			Effect:        coro.NoSuspend,
			Exec:          coro.IRQUnsafe,
			FuncRep:       coro.DirectPlain,
			Primary:       coro.PrimaryPlain,
			ManagedEntry:  coro.ManagedEntryPlain,
			PrimarySymbol: pkg + ".F",
		}},
		ExportBindings: []coro.LibraryEffectExportBinding{{
			Symbol:               pkg + "_F",
			ABIHash:              strings.Repeat("b", 64),
			Function:             functionID,
			ManagedPrimary:       coro.PrimaryPlain,
			ManagedPrimarySymbol: pkg + ".F",
		}},
	}
}

func coroArchiveTestRecord(t *testing.T, pkg string) []byte {
	t.Helper()
	record, err := coroArchiveTestSummary(t, pkg).MarshalRecord()
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func coroArchiveTestObject(t *testing.T, records []byte) []byte {
	t.Helper()
	llssa.Initialize(llssa.InitAll)
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	ctx := &context{
		buildConf: &Config{Goos: runtime.GOOS, Goarch: runtime.GOARCH},
		prog:      prog,
	}
	path := filepath.Join(t.TempDir(), coro.LibraryEffectArchiveMember)
	if err := ctx.writeCoroLibraryEffectObject(path, records); err != nil {
		t.Fatal(err)
	}
	object, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return object
}

func writeCoroArchiveTestFile(t *testing.T, members ...coroArchiveTestMember) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "library.a")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := ar.NewWriter(file)
	if err := writer.WriteGlobalHeader(); err != nil {
		file.Close()
		t.Fatal(err)
	}
	for _, member := range members {
		if err := writer.WriteHeader(&ar.Header{
			Name:    member.name,
			ModTime: time.Unix(0, 0),
			Mode:    0o644,
			Size:    int64(len(member.data)),
		}); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if _, err := writer.Write(member.data); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadCoroLibraryEffectArchive(t *testing.T) {
	record := coroArchiveTestRecord(t, "example/library")
	object := coroArchiveTestObject(t, record)
	path := writeCoroArchiveTestFile(t,
		coroArchiveTestMember{name: "payload.o", data: []byte("payload")},
		coroArchiveTestMember{name: coro.LibraryEffectArchiveMember, data: object},
	)
	summaries, found, err := ReadCoroLibraryEffectArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	if !found || len(summaries) != 1 ||
		summaries[0].Package != "example/library" ||
		len(summaries[0].Functions) != 1 ||
		len(summaries[0].ExportBindings) != 1 {
		t.Fatalf("archive summaries = %+v, found=%t", summaries, found)
	}

	absent := writeCoroArchiveTestFile(t,
		coroArchiveTestMember{name: "payload.o", data: []byte("payload")},
	)
	if summaries, found, err := ReadCoroLibraryEffectArchive(absent); err != nil ||
		found || summaries != nil {
		t.Fatalf("archive without metadata = %+v, %t, %v", summaries, found, err)
	}

	bsdPadded := writeCoroArchiveTestFile(t,
		coroArchiveTestMember{
			name: coro.LibraryEffectArchiveMember,
			data: append(append([]byte(nil), object...), '\n', '\n'),
		},
	)
	if summaries, found, err := ReadCoroLibraryEffectArchive(bsdPadded); err != nil ||
		!found || len(summaries) != 1 {
		t.Fatalf("BSD-padded archive = %+v, %t, %v", summaries, found, err)
	}
}

func TestCoroLibraryEffectObjectFormats(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	record := coroArchiveTestRecord(t, "example/library")
	tests := []struct {
		name   string
		target *llssa.Target
	}{
		{name: "ELF", target: &llssa.Target{GOOS: "linux", GOARCH: "amd64"}},
		{name: "MachO", target: &llssa.Target{GOOS: "darwin", GOARCH: "amd64"}},
		{name: "COFF", target: &llssa.Target{GOOS: "windows", GOARCH: "amd64"}},
		{name: "WebAssembly", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog := llssa.NewProgram(test.target)
			defer prog.Dispose()
			llvmContext := gllvm.NewContext()
			defer llvmContext.Dispose()
			module := llvmContext.NewModule("llgo.coro.library-effect.object-test")
			defer module.Dispose()
			if err := populateCoroLibraryEffectObjectModule(prog, module, record); err != nil {
				t.Fatal(err)
			}
			if err := gllvm.VerifyModule(module, gllvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify metadata module: %v\n%s", err, module.String())
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, gllvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit metadata object: %v\n%s", err, module.String())
			}
			defer object.Dispose()
			extracted, err := readCoroLibraryEffectObject(object.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(extracted, record) {
				t.Fatalf("extracted record length = %d, want %d", len(extracted), len(record))
			}
		})
	}
}

func TestReadCoroLibraryEffectArchiveFailsClosed(t *testing.T) {
	record := coroArchiveTestRecord(t, "example/library")
	corrupt := append([]byte(nil), record...)
	corrupt[len(corrupt)-1] ^= 1
	corruptObject := coroArchiveTestObject(t, corrupt)
	path := writeCoroArchiveTestFile(t,
		coroArchiveTestMember{name: coro.LibraryEffectArchiveMember, data: corruptObject},
	)
	if _, found, err := ReadCoroLibraryEffectArchive(path); !found || err == nil {
		t.Fatalf("corrupt archive = found %t, error %v", found, err)
	}

	object := coroArchiveTestObject(t, record)
	duplicate := writeCoroArchiveTestFile(t,
		coroArchiveTestMember{name: coro.LibraryEffectArchiveMember, data: object},
		coroArchiveTestMember{name: coro.LibraryEffectArchiveMember + "/", data: object},
	)
	if _, found, err := ReadCoroLibraryEffectArchive(duplicate); !found || err == nil ||
		!strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate archive = found %t, error %v", found, err)
	}

	excessivePadding := writeCoroArchiveTestFile(t,
		coroArchiveTestMember{
			name: coro.LibraryEffectArchiveMember,
			data: append(append([]byte(nil), object...), []byte("\n\n\n\n\n\n\n\n")...),
		},
	)
	if _, found, err := ReadCoroLibraryEffectArchive(excessivePadding); !found || err == nil {
		t.Fatalf("excessively padded archive = found %t, error %v", found, err)
	}

	twoPackages := append(
		append([]byte(nil), record...),
		coroArchiveTestRecord(t, "example/other")...,
	)
	multiple := writeCoroArchiveTestFile(t, coroArchiveTestMember{
		name: coro.LibraryEffectArchiveMember,
		data: coroArchiveTestObject(t, twoPackages),
	})
	if _, found, err := ReadCoroLibraryEffectArchive(multiple); !found || err == nil ||
		!strings.Contains(err.Error(), "want 1") {
		t.Fatalf("multi-package archive = found %t, error %v", found, err)
	}
}

func TestPackageArchiveCoroMetadataIsLinkerInvisible(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc is unavailable")
	}
	llssa.Initialize(llssa.InitAll)
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	ctx := &context{
		buildConf: &Config{Goos: runtime.GOOS, Goarch: runtime.GOARCH},
		prog:      prog,
	}
	if _, err := exec.LookPath(ctx.archiver()); err != nil {
		t.Skipf("%s is unavailable", ctx.archiver())
	}
	temp := t.TempDir()
	librarySource := filepath.Join(temp, "library.c")
	mainSource := filepath.Join(temp, "main.c")
	object := filepath.Join(temp, "library.o")
	archive := filepath.Join(temp, "library.a")
	executable := filepath.Join(temp, "main")
	if err := os.WriteFile(librarySource, []byte("int llgo_archive_value(void) { return 7; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainSource, []byte("int llgo_archive_value(void); int main(void) { return llgo_archive_value() == 7 ? 0 : 1; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(cc, "-c", librarySource, "-o", object).CombinedOutput(); err != nil {
		t.Fatalf("compile archive object: %v\n%s", err, output)
	}
	record := coroArchiveTestRecord(t, "example/library")
	collision := filepath.Join(temp, coro.LibraryEffectArchiveMember)
	collisionData, err := os.ReadFile(object)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(collision, collisionData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ctx.createPackageArchiveFile(filepath.Join(temp, "collision.a"), &aPackage{
		ObjFiles:                 []string{collision},
		CoroLibraryEffectRecords: record,
	}, false); err == nil || !strings.Contains(err.Error(), "collides with reserved") {
		t.Fatalf("reserved archive-member collision error = %v", err)
	}
	if err := ctx.createPackageArchiveFile(archive, &aPackage{
		ObjFiles:                 []string{object},
		CoroLibraryEffectRecords: record,
	}, false); err != nil {
		t.Fatal(err)
	}
	summaries, found, err := ReadCoroLibraryEffectArchive(archive)
	if err != nil || !found || len(summaries) != 1 {
		t.Fatalf("read generated package archive = %+v, %t, %v", summaries, found, err)
	}
	if output, err := exec.Command(cc, mainSource, archive, "-o", executable).CombinedOutput(); err != nil {
		t.Fatalf("link package archive containing compiler metadata: %v\n%s", err, output)
	}
	command := exec.Command(executable)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run linked package archive probe: %v\n%s", err, output)
	}

	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := ar.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	foundMember := false
	for {
		header, nextErr := reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		if strings.TrimRight(header.Name, "/ \x00") == coro.LibraryEffectArchiveMember {
			foundMember = true
		}
	}
	if !foundMember {
		t.Fatal("generated package archive omitted format-neutral coroutine metadata member")
	}
}
