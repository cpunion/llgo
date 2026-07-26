package main

import "os"

var stage uint32

//export __llgo_coro_stdlib_file_stage_v1
func stdlibFileStageV1() uint32 {
	return stage
}

func fail(code int) {
	os.Exit(code)
}

func main() {
	stage = 1
	path := "/llgo-host/stdlib-roundtrip.txt"
	if len(os.Args) > 2 {
		fail(10)
	}
	if len(os.Args) == 2 {
		path = os.Args[1]
	}
	stage = 2

	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		fail(11)
	}
	stage = 3

	want := [1]byte{'x'}
	if n, err := f.Write(want[:]); err != nil || n != len(want) {
		fail(12)
	}
	stage = 4
	if _, err := f.Seek(0, 0); err != nil {
		fail(13)
	}
	stage = 5

	var got [1]byte
	if n, err := f.Read(got[:]); err != nil || n != len(got) || got[0] != want[0] {
		fail(14)
	}
	stage = 6
	if err := f.Close(); err != nil {
		fail(15)
	}
	stage = 7
}
