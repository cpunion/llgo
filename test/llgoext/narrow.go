//go:build llgo

package llgoext

import _ "unsafe" // for go:linkname

type narrowValues struct {
	s8  int64
	u8  uint64
	s16 int64
	u16 uint64
}

//llgo:type C
type narrowFunction func(int8, uint8, int16, uint16) narrowValues

//go:linkname linkedNarrow C.narrow_promote
func linkedNarrow(int8, uint8, int16, uint16) narrowValues

//go:linkname linkedNarrowAddress C.narrow_address
func linkedNarrowAddress() narrowFunction

//llgo:type C
type narrowS8Callback func(int32) int8

//llgo:type C
type narrowU8Callback func(int32) uint8

//llgo:type C
type narrowS16Callback func(int32) int16

//llgo:type C
type narrowU16Callback func(int32) uint16

//go:linkname linkedNarrowS8Roundtrip C.narrow_s8_roundtrip
func linkedNarrowS8Roundtrip(narrowS8Callback) narrowS8Callback

//go:linkname linkedNarrowU8Roundtrip C.narrow_u8_roundtrip
func linkedNarrowU8Roundtrip(narrowU8Callback) narrowU8Callback

//go:linkname linkedNarrowS16Roundtrip C.narrow_s16_roundtrip
func linkedNarrowS16Roundtrip(narrowS16Callback) narrowS16Callback

//go:linkname linkedNarrowU16Roundtrip C.narrow_u16_roundtrip
func linkedNarrowU16Roundtrip(narrowU16Callback) narrowU16Callback
