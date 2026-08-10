package main

func main() {
	_ = max(1, 2)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
