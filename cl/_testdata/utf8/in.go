package main

import (
	"unicode/utf8"
)

func index(n int8) uint8 {
	return array[n]
}

var array = [...]uint8{
	1, 2, 3, 4, 5, 6, 7, 8,
}

func main() {
	var str = "中abcd"
	for i := 0; i < len(str); {
		r, n := utf8.DecodeRuneInString(str[i:])
		i += n
		println(r)
	}
	println(index(2) == 3)
}
