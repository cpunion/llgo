package main

import (
	"github.com/goplus/lib/c"
)

// DARWIN-ARM64: call void %__llgo_funcval_code(ptr swiftself [[CALLBACK_ENV]], ptr %0)
// LINUX-AMD64: call void %__llgo_funcval_code(ptr nest [[CALLBACK_ENV]], ptr %0)
func callback(msg *c.Char, f func(*c.Char)) {
	f(msg)
}

func main() {
	callback(c.Str("Hello\n"), print)
	callback(c.Str("callback\n"), print)
}

func print(msg *c.Char) {
	c.Printf(msg)
}
