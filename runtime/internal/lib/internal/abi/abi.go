package abi

// FuncPC* intrinsics.
//
// CAREFUL: In programs with plugins, FuncPC* can return different values
// for the same function (because there are actually multiple copies of
// the same function in the address space). To be safe, don't use the
// results of this function in any == expression. It is only safe to
// use the result as an address at which to start executing code.

// FuncPCABI0 returns the entry PC of the function f, which must be a
// direct reference of a function defined as ABI0. Otherwise it is a
// compile-time error.
//
// Implemented as a compile intrinsic.
func FuncPCABI0(f interface{}) uintptr {
	// This implementation is never called directly.
	// The compiler replaces calls to this function with machine code.
	panic("FuncPCABI0 is a compiler intrinsic and should not be called directly")
}

// FuncPCABIInternal returns the entry PC of the function f. If f is a
// direct reference of a function, it must be defined as ABIInternal.
// Otherwise it is a compile-time error. If f is not a direct reference
// of a defined function, it assumes that f is a func value. Otherwise
// the behavior is undefined.
//
// Implemented as a compile intrinsic.
func FuncPCABIInternal(f interface{}) uintptr {
	// This implementation is never called directly.
	// The compiler replaces calls to this function with machine code.
	panic("FuncPCABIInternal is a compiler intrinsic and should not be called directly")
}
