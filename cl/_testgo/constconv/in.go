package main

func main() {
	var i64 int64 = 1
	var u64 uint64 = 1
	var c int64 = i64 + (1.0 / (1.0 / 10))
	var d uint64 = u64 + (1.0 / (1.0 / 10))
	// The untyped floating-point expressions must be folded before conversion.
	println(u64, i64, c, d)
}
