package main

import (
	"fmt"
	"reflect"
	"runtime"
	"unsafe"
)

func collected(ready chan<- struct{}) {
	runtime.GC()
	runtime.GC()
	close(ready)
}

func fresh(seed byte) string {
	data := make([]byte, 32768)
	for index := range data {
		data[index] = seed
	}
	return string(data)
}

func aliasAfterGC(value string) byte {
	header := (*reflect.StringHeader)(unsafe.Pointer(&value))
	word := header.Data
	ready := make(chan struct{})
	go collected(ready)
	<-ready
	return *(*byte)(unsafe.Pointer(word))
}

func copyAfterGC(value string) byte {
	header := *(*reflect.StringHeader)(unsafe.Pointer(&value))
	word := header.Data
	ready := make(chan struct{})
	go collected(ready)
	<-ready
	return *(*byte)(unsafe.Pointer(word))
}

type pointerer interface{ Pointer() uintptr }

func reflectAfterGC(kind int, value byte) byte {
	reflected := reflect.ValueOf(&value)
	var pointer unsafe.Pointer
	switch kind {
	case 0:
		pointer = unsafe.Pointer(reflected.Pointer())
	case 1:
		pointer = unsafe.Pointer(reflect.Value.Pointer(reflected))
	case 2:
		var object pointerer = reflected
		pointer = unsafe.Pointer(object.Pointer())
	case 3:
		method := reflected.Pointer
		pointer = unsafe.Pointer(method())
	case 4:
		pointer = unsafe.Pointer(reflected.Elem().UnsafeAddr())
	}
	ready := make(chan struct{})
	go collected(ready)
	<-ready
	return *(*byte)(pointer)
}

var values []byte

func globalHeaderAfterGC(value byte) byte {
	values = make([]byte, 32768)
	values[0] = value
	header := (*reflect.SliceHeader)(unsafe.Pointer(&values))
	pointer := unsafe.Pointer(header.Data)
	// The mutable global cell is no longer an owner. Only the reconstructed
	// pointer may keep the old backing store alive across collection and await.
	values = nil
	ready := make(chan struct{})
	go collected(ready)
	<-ready
	return *(*byte)(pointer)
}

func main() {
	for iteration := byte(1); iteration <= 8; iteration++ {
		if aliasAfterGC(fresh(iteration)) != iteration || copyAfterGC(fresh(iteration+8)) != iteration+8 {
			panic("reflect header lost its string owner across GC/await")
		}
		for kind := 0; kind < 5; kind++ {
			if reflectAfterGC(kind, iteration) != iteration {
				panic("reflect address result lost its owner across GC/await")
			}
		}
		if globalHeaderAfterGC(iteration) != iteration {
			panic("global header pointer lost its old backing store across GC/await")
		}
	}
	fmt.Println("reflect header exact owner survives GC and await")
}
