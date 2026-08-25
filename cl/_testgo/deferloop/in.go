package main

// A defer in a loop stores the current iteration value in each LIFO node.
func main() {
	for i := 0; i < 3; i++ {
		defer println("loop", i)
	}
}
