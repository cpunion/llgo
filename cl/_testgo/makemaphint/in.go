package main

func fromInt32(n int32) int {
	return len(make(map[string]int, n))
}

func fromUint32(n uint32) int {
	return len(make(map[string]int, n))
}

func main() {
	println(fromUint32(2))
	println(fromInt32(3))
}
