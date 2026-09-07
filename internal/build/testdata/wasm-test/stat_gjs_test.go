//go:build js && wasm && !llgo.wasm.emscripten

package wasmtest

import (
	"os"
	"path/filepath"
	"syscall"
	"syscall/js"
	"testing"
	"time"
)

func TestGJSStatWideNumbers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stat.txt")
	if err := os.WriteFile(path, []byte("stat"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, seconds := range []int64{31536000, 1577923200, 2208988800} {
		want := time.Unix(seconds, 0)
		if err := os.Chtimes(path, want, want); err != nil {
			t.Fatal(err)
		}
		var st syscall.Stat_t
		if err := syscall.Stat(path, &st); err != nil {
			t.Fatal(err)
		}
		if st.Atime != seconds || st.Mtime != seconds {
			t.Fatalf("stat times = %d, %d; want %d", st.Atime, st.Mtime, seconds)
		}
		host := js.Global().Get("fs").Call("statSync", path)
		if st.Ino != uint64(host.Get("ino").Float()) || st.Dev != int64(host.Get("dev").Float()) {
			t.Fatalf("stat identity truncated: dev=%d ino=%d", st.Dev, st.Ino)
		}
	}
}

func TestGJSStatWideHostFields(t *testing.T) {
	// Supply deterministic wide fields through the same asynchronous fs.stat
	// boundary, without creating a multi-gigabyte fixture on a CI runner.
	fs := js.Global().Get("fs")
	original := fs.Get("stat")
	const wide = int64(1<<40 + 17)
	callback := js.FuncOf(func(_ js.Value, args []js.Value) any {
		args[len(args)-1].Invoke(js.Null(), map[string]any{
			"dev": wide, "ino": wide + 1, "rdev": wide + 2, "size": wide + 3,
			"mode": 0600, "nlink": 1, "uid": uint32(1<<32 - 2), "gid": uint32(1<<32 - 3),
			"blksize": 4096, "blocks": 7,
			"atimeMs": wide, "mtimeMs": wide + 1, "ctimeMs": wide + 2,
		})
		return nil
	})
	defer callback.Release()
	fs.Set("stat", callback)
	defer fs.Set("stat", original)
	var st syscall.Stat_t
	if err := syscall.Stat("wide-stat-fixture", &st); err != nil {
		t.Fatal(err)
	}
	if st.Dev != wide || st.Ino != uint64(wide+1) || st.Rdev != wide+2 || st.Size != wide+3 ||
		st.Uid != 1<<32-2 || st.Gid != 1<<32-3 {
		t.Fatalf("wide stat fields truncated: %+v", st)
	}
	if st.Atime*1000+st.AtimeNsec/1000000 != wide ||
		st.Mtime*1000+st.MtimeNsec/1000000 != wide+1 ||
		st.Ctime*1000+st.CtimeNsec/1000000 != wide+2 {
		t.Fatalf("wide stat timestamps truncated: %+v", st)
	}
}
