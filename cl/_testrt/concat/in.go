package main

// The loop carries both the accumulated string and the current index. The
// element selected by that index must be concatenated into the carried value.
func concat(args ...string) (ret string) {
	for _, v := range args {
		ret += v
	}
	return
}

func info(s string) string {
	return "" + s + "..."
}

func main() {
	result := concat("Hello", " ", "World")
	println(result)
}
