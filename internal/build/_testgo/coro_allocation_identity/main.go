package main

func ArrayHistory(limit int) int {
	var pointers [2]*int
	for i := 0; i < limit; i++ {
		x := i
		pointers[i] = &x
	}
	if pointers[0] == pointers[1] {
		panic("array retained repeated allocation identity")
	}
	return *pointers[0] + *pointers[1]
}

func SelectHistory(limit int) int {
	channel := make(chan int, 1)
	var pointers [2]*int
	for i := 0; i < limit; i++ {
		channel <- i
		select {
		case x := <-channel:
			pointers[i] = &x
		}
	}
	if pointers[0] == pointers[1] {
		panic("select retained repeated allocation identity")
	}
	return *pointers[0] + *pointers[1]
}

func PhiHistory(limit int) bool {
	var previous *int
	equal := false
	for i := 0; i < limit; i++ {
		x := i
		if previous != nil {
			equal = equal || previous == &x
		}
		previous = &x
	}
	return equal
}

func Observe(value *int) int { return *value }

func ImmediateBorrow(limit int) int {
	result := 0
	for i := 0; i < limit; i++ {
		x := i
		result += Observe(&x)
	}
	return result
}

func main() {
	if ArrayHistory(2) != 1 || SelectHistory(2) != 1 || PhiHistory(2) || ImmediateBorrow(2) != 1 {
		panic("repeated allocation value mismatch")
	}
	println("repeated allocation identity ok")
}
