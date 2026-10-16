package main

import "syscall"

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	const directory = "/tmp/llgo-coro-wasi-filesystem"
	must(syscall.Mkdir(directory, 0700))
	old, err := syscall.Getwd()
	must(err)
	must(syscall.Chdir(directory))
	if cwd, err := syscall.Getwd(); err != nil || cwd != directory {
		panic("Go cwd was not preserved")
	}
	dir, err := syscall.Open(".", syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	must(err)
	fd, err := syscall.Openat(dir, "first", syscall.O_RDWR|syscall.O_CREAT|syscall.O_TRUNC, 0600)
	must(err)
	if n, err := syscall.Write(fd, []byte("abcdef")); err != nil || n != 6 {
		panic("fd-relative open/write")
	}
	must(syscall.Ftruncate(fd, 4))
	must(syscall.Fsync(fd))
	var st syscall.Stat_t
	must(syscall.Fstat(fd, &st))
	if st.Size != 4 {
		panic("fstat/truncate")
	}
	must(syscall.Close(fd))
	must(syscall.Rename("first", "renamed"))
	must(syscall.Link("renamed", "hard"))
	must(syscall.Symlink("renamed", "soft"))
	var link [32]byte
	if n, err := syscall.Readlink("soft", link[:]); err != nil || string(link[:n]) != "renamed" {
		panic("readlink")
	}
	must(syscall.Lstat("soft", &st))
	if st.Filetype != syscall.FILETYPE_SYMBOLIC_LINK {
		panic("lstat followed link")
	}
	must(syscall.Stat("soft", &st))
	if st.Size != 4 || st.Filetype != syscall.FILETYPE_REGULAR_FILE {
		panic("stat did not follow link")
	}
	// Nonzero high words in both timestamps exercise the wasm32 split ABI.
	ts := []syscall.Timespec{{Sec: 1700000000, Nsec: 123456789}, {Sec: 1700000010, Nsec: 987654321}}
	must(syscall.UtimesNano("renamed", ts))
	must(syscall.Stat("renamed", &st))
	if st.Atime != uint64(syscall.TimespecToNsec(ts[0])) || st.Mtime != uint64(syscall.TimespecToNsec(ts[1])) {
		panic("64-bit timestamp transport")
	}
	var entries [1024]byte
	if n, err := syscall.ReadDir(dir, entries[:], 0); err != nil || n == 0 {
		panic("directory read")
	}
	fd, err = syscall.Open("renamed", syscall.O_RDONLY, 0)
	must(err)
	var contents [4]byte
	if n, err := syscall.Read(fd, contents[:]); err != nil || n != 4 || string(contents[:]) != "abcd" {
		panic("relative Open used a different cwd")
	}
	must(syscall.Close(fd))
	must(syscall.Close(dir))
	must(syscall.Unlink("soft"))
	must(syscall.Unlink("hard"))
	must(syscall.Unlink("renamed"))
	must(syscall.Chdir(old))
	must(syscall.Rmdir(directory))
	const success = "WASI raw filesystem and Go cwd ok\n"
	if n, err := syscall.Write(1, []byte(success)); err != nil || n != len(success) {
		panic("stdout marker")
	}
}
