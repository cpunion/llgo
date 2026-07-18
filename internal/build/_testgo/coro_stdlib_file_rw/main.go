package main

import "os"

func fail(code int) {
	os.Exit(code)
}

func main() {
	if len(os.Args) != 2 {
		fail(10)
	}

	f, err := os.OpenFile(os.Args[1], os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		fail(11)
	}

	want := []byte("llgo synchronous os.File read/write\n")
	written := 0
	for written < len(want) {
		n, err := f.Write(want[written:])
		if n > 0 {
			written += n
		}
		if err != nil || n == 0 {
			fail(12)
		}
	}
	if _, err := f.Seek(0, 0); err != nil {
		fail(13)
	}

	got := make([]byte, len(want))
	read := 0
	for read < len(got) {
		n, err := f.Read(got[read:])
		if n > 0 {
			read += n
		}
		if err != nil || n == 0 {
			fail(14)
		}
	}
	for i := range want {
		if got[i] != want[i] {
			fail(15)
		}
	}
	if err := f.Close(); err != nil {
		fail(16)
	}
}
