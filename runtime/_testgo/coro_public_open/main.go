//go:build darwin || linux

package main

import (
	"os"
	"runtime"
	"syscall"
	_ "unsafe"
)

const LLGoFiles = "wrap/public_open.c"

//go:linkname install C.llgo_public_open_install
func install(*byte, uint32) int32

//llgo:coro noblock
//go:linkname entered C.llgo_public_open_entered
func entered() uint32

//llgo:coro noblock
//go:linkname fixtureMode C.llgo_public_open_mode
func fixtureMode() uint32

//llgo:coro noblock
//go:linkname writerDone C.llgo_public_open_writer_done
func writerDone()

//llgo:coro noblock
//go:linkname releaseFileStage C.llgo_public_open_release_file_stage
func releaseFileStage()

func sentinel(path string, ready chan<- struct{}) {
	defer func() {
		if fixtureMode() >= 8 {
			releaseFileStage()
			return
		}
		for {
			fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
			if err == syscall.ENXIO {
				runtime.Gosched()
				continue
			}
			if err != nil || fd < 0 {
				panic("FIFO cleanup writer failed")
			}
			if syscall.Close(fd) != nil {
				panic("FIFO cleanup writer close failed")
			}
			writerDone()
			return
		}
	}()
	close(ready)
	var never chan struct{}
	<-never
}

func read(open func(string, int, uint32) (int, error), path string) {
	fd, err := open(path, syscall.O_RDONLY, 0)
	if err == nil {
		_ = syscall.Close(fd)
	}
	panic("canceled public Open executed normal continuation")
}

func deferredRead(path string) { defer syscall.Open(path, syscall.O_RDONLY, 0) }

func readFileWith(open func(string) (*os.File, error), path string) {
	f, err := open(path)
	if err == nil {
		_ = f.Close()
	}
	panic("canceled File construction executed normal continuation")
}

func readFile(path string) { readFileWith(os.Open, path) }

func deferredFile(path string) { defer os.Open(path) }

func earlyWriter(path string) {
	for {
		fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == syscall.ENXIO {
			runtime.Gosched()
			continue
		}
		if err != nil || syscall.Close(fd) != nil {
			panic("early FIFO writer failed")
		}
		writerDone()
		return
	}
}

func checkNormalFile() {
	open := os.Open
	f, err := open("/dev/null")
	if err != nil || f == nil || f.Name() != "/dev/null" {
		panic("normal File delivery failed")
	}
	runtime.GC()
	var st syscall.Stat_t
	if syscall.Fstat(int(f.Fd()), &st) != nil || f.Close() != nil {
		panic("normal File ownership changed")
	}
	f, err = os.OpenFile("/dev/null", os.O_WRONLY|os.O_APPEND, 0)
	if err != nil || f == nil {
		panic("normal OpenFile failed")
	}
	if _, err = f.WriteAt([]byte{1}, 0); err == nil {
		panic("OpenFile lost append mode")
	}
	if f.Close() != nil {
		panic("normal OpenFile close failed")
	}
	f, err = os.Open("invalid\x00path")
	pathErr, ok := err.(*os.PathError)
	if f != nil || !ok || pathErr.Op != "open" || pathErr.Err != syscall.EINVAL {
		panic("File path validation changed")
	}
}

func main() {
	var bytes [128]byte
	if install(&bytes[0], uint32(len(bytes))) != 0 {
		panic("public Open fixture installation failed")
	}
	n := 0
	for n < len(bytes) && bytes[n] != 0 {
		n++
	}
	if n == len(bytes) {
		panic("unterminated fixture path")
	}
	path := string(bytes[:n])
	checkNormalFile()
	fd, err := syscall.Open("/dev/null", syscall.O_RDONLY, 0)
	if err != nil || fd < 0 {
		panic("normal public Open failed")
	}
	var info syscall.Stat_t
	if syscall.Fstat(fd, &info) != nil {
		panic("normal delivery closed its fd")
	}
	if syscall.Close(fd) != nil {
		panic("normal public close failed")
	}
	fd, err = syscall.Open("invalid\x00path", syscall.O_RDONLY, 0)
	if fd != 0 || err != syscall.EINVAL {
		panic("invalid path validation changed")
	}
	go syscall.Open("invalid\x00path", syscall.O_RDONLY, 0)
	ready := make(chan struct{})
	go sentinel(path, ready)
	<-ready
	switch fixtureMode() {
	case 0:
		go func() {
			fd, err := syscall.Open(path, syscall.O_RDONLY, 0)
			if err == nil {
				_ = syscall.Close(fd)
			}
			panic("canceled static Open executed normal continuation")
		}()
	case 1:
		go syscall.Open(path, syscall.O_RDONLY, 0)
	case 2:
		go read(syscall.Open, path)
	case 3:
		go deferredRead(path)
	case 4:
		go readFile(path)
	case 5:
		go os.Open(path)
	case 6:
		go os.OpenFile(path, os.O_RDONLY, 0)
	case 7:
		go deferredFile(path)
	case 8:
		if runtime.GOOS != "darwin" {
			panic("File stat-stage fixture requires Darwin")
		}
		go readFile(path)
		go earlyWriter(path)
	case 9:
		if runtime.GOOS != "darwin" {
			panic("File stat-stage fixture requires Darwin")
		}
		go os.Open(path)
		go earlyWriter(path)
	case 10:
		if runtime.GOOS != "darwin" {
			panic("File stat-stage fixture requires Darwin")
		}
		go readFileWith(os.Open, path)
		go earlyWriter(path)
	default:
		panic("unknown public Open fixture mode")
	}
	// The libc shim signals physical entry before blocking on the real FIFO.
	// Its writer exists only in cancellation cleanup, so completion cannot race
	// ahead of the command's cancellation request.
	for entered()&1 == 0 || fixtureMode() >= 8 && entered()&6 != 6 {
		runtime.Gosched()
	}
	runtime.GC()
}
