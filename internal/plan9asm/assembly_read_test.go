package plan9asm

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

type assemblyCountingSource struct {
	io.Reader
	readBytes int
	closed    bool
	closeErr  error
}

func (source *assemblyCountingSource) Read(buffer []byte) (int, error) {
	n, err := source.Reader.Read(buffer)
	source.readBytes += n
	return n, err
}

func (source *assemblyCountingSource) Close() error {
	source.closed = true
	return source.closeErr
}

func TestAssemblyReadsEnforceBoundDuringRead(t *testing.T) {
	const limit = 1024
	for _, size := range []int{0, limit, limit + 1, 4 * limit} {
		source := &assemblyCountingSource{Reader: bytes.NewReader(make([]byte, size))}
		body, err := readAssemblyStreamBounded(source, limit)
		if (err == nil) != (size <= limit) || size <= limit && len(body) != size {
			t.Errorf("size %d: %d bytes, %v", size, len(body), err)
		}
		if source.readBytes > limit+1 {
			t.Errorf("read %d bytes before enforcing %d-byte bound", source.readBytes, limit)
		}
		if !source.closed {
			t.Fatal("source not closed")
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "sparse.s")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(limit + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readAssemblyFileBounded(nil, path, limit); err == nil {
		t.Fatal("sparse source above limit accepted")
	}
	if _, err := readAssemblyFileBounded(map[string][]byte{path: make([]byte, limit+1)}, path, limit); err == nil {
		t.Fatal("oversized overlay accepted")
	}
	if got, err := readAssemblyFileBounded(map[string][]byte{path: []byte("exact")}, path, 5); err != nil || string(got) != "exact" {
		t.Fatalf("bounded overlay: %q, %v", got, err)
	}
}

func TestAssemblyReadsPreserveCloseAndReadErrors(t *testing.T) {
	closeErr := errors.New("injected close failure")
	source := &assemblyCountingSource{Reader: strings.NewReader("small"), closeErr: closeErr}
	if body, err := readAssemblyStreamBounded(source, 8); body != nil || !errors.Is(err, closeErr) {
		t.Fatalf("close error discarded: %q, %v", body, err)
	}
	source = &assemblyCountingSource{Reader: strings.NewReader("over-limit"), closeErr: closeErr}
	if body, err := readAssemblyStreamBounded(source, 1); body != nil || !errors.Is(err, closeErr) || !strings.Contains(err.Error(), "inventory bound") {
		t.Fatalf("close/limit errors lost: %q, %v", body, err)
	}
	readErr := errors.New("injected read failure")
	source = &assemblyCountingSource{Reader: iotest.ErrReader(readErr), closeErr: closeErr}
	if body, err := readAssemblyStreamBounded(source, 8); body != nil || !errors.Is(err, readErr) || !errors.Is(err, closeErr) || !source.closed {
		t.Fatalf("read/close errors lost: %q, %v; closed=%v", body, err, source.closed)
	}
	for _, limit := range []int{0, -1} {
		source = &assemblyCountingSource{Reader: strings.NewReader("x")}
		if body, err := readAssemblyStreamBounded(source, limit); body != nil || err == nil || !source.closed || source.readBytes > limit+1 {
			t.Fatalf("limit %d: %q, %v; bytes=%d, closed=%v", limit, body, err, source.readBytes, source.closed)
		}
	}
}

func TestAssemblyReadsShareSourceAndIncludeQuota(t *testing.T) {
	pkg := mustTestPackage(t, "example.org/quota", "package quota")
	pkg.Dir = t.TempDir()
	file := filepath.Join(pkg.Dir, "source.s")
	headerFile := filepath.Join(pkg.Dir, "shared.h")
	// The guard prevents duplicate definitions, not repeated file reads. Both
	// active includes must consume their bytes from the same source graph quota.
	header := []byte("#ifndef SHARED\n#define SHARED 1\n#endif\n")
	source := []byte("#include \"shared.h\"\n#include \"shared.h\"\nRET\n")
	if err := os.WriteFile(headerFile, header, 0600); err != nil {
		t.Fatal(err)
	}
	limit := len(source) + 2*len(header)
	for _, overlay := range []map[string][]byte{nil, {headerFile: header}} {
		got, err := preprocessAssemblyForPkgWithLimit(pkg, file, source, overlay, "linux", "amd64", TranslateOptions{}, limit)
		if err != nil || strings.TrimSpace(string(got)) != "RET" {
			t.Fatalf("exact graph quota: %q, %v", got, err)
		}
		if _, err := preprocessAssemblyForPkgWithLimit(pkg, file, source, overlay, "linux", "amd64", TranslateOptions{}, limit-1); err == nil || !strings.Contains(err.Error(), "inventory bound") {
			t.Fatalf("repeated include escaped shared quota: %v", err)
		}
	}
	if got, err := preprocessAssemblyForPkgWithLimit(pkg, file, []byte("RET\n"), nil, "linux", "amd64", TranslateOptions{}, 4); err != nil || string(got) != "RET\n" {
		t.Fatalf("exact source quota: %q, %v", got, err)
	}
	if _, err := preprocessAssemblyForPkgWithLimit(pkg, file, []byte("RET\n\n"), nil, "linux", "amd64", TranslateOptions{}, 4); err == nil {
		t.Fatal("oversized initial source accepted")
	}
	inactive := []byte("#ifdef ABSENT\n#include \"shared.h\"\n#endif\nRET\n")
	if got, err := preprocessAssemblyForPkgWithLimit(pkg, file, inactive, nil, "linux", "amd64", TranslateOptions{SourceGOROOT: "missing-toolchain"}, len(inactive)); err != nil || strings.TrimSpace(string(got)) != "RET" {
		t.Fatalf("inactive include consumed graph quota or performed I/O: %q, %v", got, err)
	}
}
