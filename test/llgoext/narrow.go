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
