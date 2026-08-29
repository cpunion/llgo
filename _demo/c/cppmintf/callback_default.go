//go:build !windows || !386

package main

import "github.com/goplus/lib/c"

func callbackCalc() c.Pointer {
	return c.Func((*Bar).sqrt)
}

func callbackVal() c.Pointer {
	return c.Func(bar_IVal_getA)
}
