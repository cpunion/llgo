//go:build llgo && wasm

package math

// LLGo does not consume Go's WebAssembly assembly ABI. Keep the standard
// library's generic algorithms, including signed zero, NaN and infinities.
const haveArchFloor = false
const haveArchCeil = false
const haveArchTrunc = false

func archFloor(x float64) float64 { return floor(x) }
func archCeil(x float64) float64  { return ceil(x) }
func archTrunc(x float64) float64 { return trunc(x) }
