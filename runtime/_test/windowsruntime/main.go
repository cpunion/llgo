package main

import (
	nativesync "github.com/goplus/llgo/runtime/internal/clite/sync"
	// The smoke package lives below the LLGo runtime root, whose packages are
	// excluded from the ordinary need-runtime signal. Import the core runtime
	// explicitly so its global state is initialized before the low-level test.
	_ "github.com/goplus/llgo/runtime/internal/runtime"
)

func checkRecover() {
	defer func() {
		if value := recover(); value != "windows panic smoke" {
			panic("wrong recovered value")
		}
	}()
	panic("windows panic smoke")
}

func main() {
	values := make(chan int)
	go func() {
		values <- 42
	}()
	if value := <-values; value != 42 {
		panic("wrong channel value")
	}

	var once nativesync.Once
	done := make(chan struct{}, 4)
	onceValue := 0
	for i := 0; i < 4; i++ {
		go func() {
			if result := once.Do(func() { onceValue = 7 }); result != 0 {
				panic("native once failed")
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
	if onceValue != 7 {
		panic("native once ran incorrectly")
	}

	checkRecover()
	println("windows runtime smoke: ok")
}
