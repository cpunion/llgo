package main

import "time"

func main() {
	if time.Now().Unix() <= 0 {
		panic("invalid clock")
	}
}
