//go:build tinygo

package main

/*
#include <stdio.h>

static void printHello(void) {
	printf("Hello, world\n");
}
*/
import "C"

func main() {
	C.printHello()
}
