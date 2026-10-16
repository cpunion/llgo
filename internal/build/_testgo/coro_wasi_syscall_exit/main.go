package main

import "syscall"

func main() {
	syscall.Exit(42)
	panic("syscall.Exit returned")
}
