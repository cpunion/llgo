package main

var sink int32

//go:noinline
func increment(value int32) int32 {
	result := value + 1
	sink = result
	return result
}

func main() {
	value := int32(41)
	result := increment(value)
	println(result)
}
