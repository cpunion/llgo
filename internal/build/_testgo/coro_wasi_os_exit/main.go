package main

import "os"

func main() {
	os.Exit(42)
	panic("os.Exit returned")
}
