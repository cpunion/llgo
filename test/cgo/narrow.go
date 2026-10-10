//go:build llgo && !wasm

package cgo

/*
// Keep real calls across the C boundary even when LTO is enabled.
static __attribute__((noinline)) int narrow_s8(signed char x) { return x; }
static __attribute__((noinline)) unsigned int narrow_u8(unsigned char x) { return x; }
static __attribute__((noinline)) int narrow_s16(short x) { return x; }
static __attribute__((noinline)) unsigned int narrow_u16(unsigned short x) { return x; }

static void *narrow_s8_address(void) { return (void *)narrow_s8; }
static void *narrow_u8_address(void) { return (void *)narrow_u8; }
static void *narrow_s16_address(void) { return (void *)narrow_s16; }
static void *narrow_u16_address(void) { return (void *)narrow_u16; }
*/
import "C"

import "unsafe"

//llgo:type C
type narrowS8 func(int8) int32

//llgo:type C
type narrowU8 func(uint8) uint32

//llgo:type C
type narrowS16 func(int16) int32

//llgo:type C
type narrowU16 func(uint16) uint32

func directNarrow(a int8, b uint8, c int16, d uint16) (int32, uint32, int32, uint32) {
	return int32(C.narrow_s8(C.schar(a))), uint32(C.narrow_u8(C.uchar(b))),
		int32(C.narrow_s16(C.short(c))), uint32(C.narrow_u16(C.ushort(d)))
}

func directNarrowConstants() (int32, uint32, int32, uint32) {
	return int32(C.narrow_s8(-8)), uint32(C.narrow_u8(250)),
		int32(C.narrow_s16(-300)), uint32(C.narrow_u16(60000))
}

func indirectNarrowConstants() (int32, uint32, int32, uint32) {
	sa, ua, sb, ub := C.narrow_s8_address(), C.narrow_u8_address(), C.narrow_s16_address(), C.narrow_u16_address()
	s8 := *(*narrowS8)(unsafe.Pointer(&sa))
	u8 := *(*narrowU8)(unsafe.Pointer(&ua))
	s16 := *(*narrowS16)(unsafe.Pointer(&sb))
	u16 := *(*narrowU16)(unsafe.Pointer(&ub))
	return s8(-8), u8(250), s16(-300), u16(60000)
}

func indirectNarrow(a int8, b uint8, c int16, d uint16) (int32, uint32, int32, uint32) {
	sa, ua, sb, ub := C.narrow_s8_address(), C.narrow_u8_address(), C.narrow_s16_address(), C.narrow_u16_address()
	s8 := *(*narrowS8)(unsafe.Pointer(&sa))
	u8 := *(*narrowU8)(unsafe.Pointer(&ua))
	s16 := *(*narrowS16)(unsafe.Pointer(&sb))
	u16 := *(*narrowU16)(unsafe.Pointer(&ub))
	return s8(a), u8(b), s16(c), u16(d)
}
