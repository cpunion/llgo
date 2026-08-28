package main

import "github.com/goplus/lib/c"

// Native setjmp retains one stack activation and is deliberately rejected
// when a caller would otherwise become a stackless coroutine. Target-specific
// setjmp ABI spelling is covered by ssa/eh_patch_test.go instead of asserting
// the obsolete native lowering here.
func main() {
	jb := c.AllocaSigjmpBuf()
	switch ret := c.Sigsetjmp(jb, 0); ret {
	case 0:
		cstr := c.Str("??Hello, setjmp!\n")
		c.Fprintf(c.Stderr, c.Str("%s"), c.Advance(c.Pointer(c.Advance(cstr, 1)), 1))
		c.Siglongjmp(jb, 1)
	default:
		println("exception:", ret)
	}
}
