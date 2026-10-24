//go:build linux || darwin

package gotest

import (
	"os/exec"
	"strings"
	"testing"
)

// The last byte of each returned value is immediately before an inaccessible
// page. Reading the ABI carrier width from the source would cross that page.
func TestAggregateReturnAtProtectedBoundary(t *testing.T) {
	const source = `package main

import (
	"syscall"
	"unsafe"
)

//go:noinline
func read9(p *[9]byte) [9]byte { return *p }
//go:noinline
func read12(p *[12]byte) [12]byte { return *p }
//go:noinline
func read15(p *[15]byte) [15]byte { return *p }
//go:noinline
func read8(p *[8]byte) [8]byte { return *p }

//go:noinline
func snapshot9(p *[9]byte) [9]byte {
	v := *p
	p[0] ^= 0xff
	return v
}

func main() {
	size := syscall.Getpagesize()
	data, err := syscall.Mmap(-1, 0, 2*size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil { panic(err) }
	defer syscall.Munmap(data)
	if err := syscall.Mprotect(data[size:], syscall.PROT_NONE); err != nil { panic(err) }
	var checksum uint64
	for round := 0; round < 64; round++ {
		for i := size-32; i < size; i++ { data[i] = byte(i+round) }
		a := read9((*[9]byte)(unsafe.Pointer(&data[size-9])))
		b := read12((*[12]byte)(unsafe.Pointer(&data[size-12])))
		c := read15((*[15]byte)(unsafe.Pointer(&data[size-15])))
		d := read8((*[8]byte)(unsafe.Pointer(&data[size-9])))
		for i, v := range a { if v != data[size-9+i] { panic("nine-byte return") }; checksum += uint64(v) }
		for i, v := range b { if v != data[size-12+i] { panic("twelve-byte return") }; checksum += uint64(v) }
		for i, v := range c { if v != data[size-15+i] { panic("fifteen-byte return") }; checksum += uint64(v) }
		for i, v := range d { if v != data[size-9+i] { panic("unaligned return") }; checksum += uint64(v) }
		s := snapshot9((*[9]byte)(unsafe.Pointer(&data[size-9])))
		if s != a || data[size-9] != a[0]^0xff { panic("captured return") }
	}
	println("ok boundary", checksum)
}
`
	_, dir := prepareCallerAcceptanceProbe(t, source)
	var expected string
	for _, tool := range []string{"go", "llgo"} {
		image := testExecutablePath(dir, tool+"-return-boundary")
		compiler := tool
		args := []string{"build", "-p=1", "-o", image, "main.go"}
		if tool == "llgo" {
			compiler = acceptanceLLGoBinary(t)
			args = append([]string{"build", "-Os"}, args[1:]...)
		}
		cmd := exec.Command(compiler, args...)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", tool, err, output)
		}
		cmd = exec.Command(image)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("run %s: %v\n%s", tool, err, output)
		}
		actual := strings.TrimSpace(string(output))
		if tool == "go" {
			expected = actual
			if !strings.HasPrefix(expected, "ok boundary ") {
				t.Fatal(expected)
			}
		} else if actual != expected {
			t.Fatalf("LLGo=%q Go=%q", actual, expected)
		}
	}
}
