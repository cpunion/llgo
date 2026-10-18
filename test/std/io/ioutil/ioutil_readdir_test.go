package ioutil_test

import (
	"io"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

func TestReadDir(t *testing.T) {
	dir := t.TempDir()
	names := []string{"alpha.txt", "beta.txt", "gopher"}
	for _, name := range names[:2] {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatalf("WriteFile %s error: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, names[2]), 0o755); err != nil {
		t.Fatalf("Mkdir %s error: %v", names[2], err)
	}

	infos, err := ioutil.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir error: %v", err)
	}
	if len(infos) != len(names) {
		t.Fatalf("ReadDir returned %d entries, want %d", len(infos), len(names))
	}

	got := make(map[string]fs.FileInfo)
	for _, info := range infos {
		got[info.Name()] = info
	}
	for i, name := range names {
		if _, ok := got[name]; !ok {
			t.Fatalf("ReadDir missing entry %s", name)
		}
		if infos[i].Name() != name {
			t.Fatalf("ReadDir entry %d = %q, want sorted name %q", i, infos[i].Name(), name)
		}
	}
	if !got[names[2]].IsDir() {
		t.Fatalf("ReadDir expected %s to be directory", names[2])
	}
}

func TestReaddirBatches(t *testing.T) {
	dir := t.TempDir()
	want := map[string]int64{"first": 3, "second": 5, "third": 7}
	for name, size := range want {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for len(want) != 0 {
		infos, err := f.Readdir(1)
		if err != nil || len(infos) != 1 {
			t.Fatalf("Readdir(1) = %v, %v", infos, err)
		}
		info := infos[0]
		size, ok := want[info.Name()]
		if !ok || info.Size() != size || !info.Mode().IsRegular() {
			t.Fatalf("unexpected directory entry: %s size=%d mode=%v", info.Name(), info.Size(), info.Mode())
		}
		delete(want, info.Name())
	}
	if infos, err := f.Readdir(1); len(infos) != 0 || err != io.EOF {
		t.Fatalf("exhausted Readdir(1) = %v, %v; want EOF", infos, err)
	}
	if infos, err := f.Readdir(-1); len(infos) != 0 || err != nil {
		t.Fatalf("exhausted Readdir(-1) = %v, %v; want empty with no error", infos, err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Readdir(1); err == nil {
		t.Fatal("Readdir on a closed directory succeeded")
	}
}
