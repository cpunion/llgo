//go:build windows && 386

package main

import (
	"github.com/goplus/lib/c"
	"github.com/xgo-dev/llgo/_demo/c/cppmintf/foo"
)

func callbackCalc() c.Pointer {
	return foo.Windows386CalcThunk()
}

func callbackVal() c.Pointer {
	return foo.Windows386ValThunk()
}

//export llgo_cppmintf_calc_cdecl
func llgo_cppmintf_calc_cdecl(this c.Pointer, value float64) float64 {
	return (*Bar)(this).sqrt(value)
}

//export llgo_cppmintf_val_cdecl
func llgo_cppmintf_val_cdecl(this c.Pointer) c.Int {
	return bar_IVal_getA(this)
}
