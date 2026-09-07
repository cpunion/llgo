//go:build wasip1 && wasm

package wasmtest

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func TestWASIFileStatHostLayout(t *testing.T) {
	var st syscall.Stat_t
	if unsafe.Offsetof(st.Nlink) != 24 || unsafe.Offsetof(st.Size) != 32 ||
		unsafe.Offsetof(st.Atime) != 40 || unsafe.Offsetof(st.Mtime) != 48 || unsafe.Offsetof(st.Ctime) != 56 {
		t.Fatalf("WASI filestat offsets: nlink=%d size=%d atime=%d mtime=%d ctime=%d",
			unsafe.Offsetof(st.Nlink), unsafe.Offsetof(st.Size), unsafe.Offsetof(st.Atime), unsafe.Offsetof(st.Mtime), unsafe.Offsetof(st.Ctime))
	}
	path := filepath.Join(t.TempDir(), "stat.txt")
	const contents = "WASI file metadata"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	want := time.Unix(1577923200, 0)
	if err := os.Chtimes(path, want, want); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, stat := range []func() error{
		func() error { return syscall.Stat(path, &st) },
		func() error { return syscall.Lstat(path, &st) },
		func() error { return syscall.Fstat(int(f.Fd()), &st) },
	} {
		if err := stat(); err != nil {
			t.Fatal(err)
		}
		if st.Filetype != syscall.FILETYPE_REGULAR_FILE || st.Size != uint64(len(contents)) || st.Nlink == 0 || st.Mtime != uint64(want.UnixNano()) {
			t.Fatalf("invalid WASI metadata: %+v", st)
		}
	}
}
