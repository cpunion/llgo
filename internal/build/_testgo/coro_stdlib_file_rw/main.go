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

	want := [1]byte{'x'}
	if n, err := f.Write(want[:]); err != nil || n != len(want) {
		fail(12)
	}
	if _, err := f.Seek(0, 0); err != nil {
		fail(13)
	}

	var got [1]byte
	if n, err := f.Read(got[:]); err != nil || n != len(got) || got[0] != want[0] {
		fail(14)
	}
	if err := f.Close(); err != nil {
		fail(15)
	}
}
