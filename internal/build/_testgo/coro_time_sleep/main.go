package main

import "time"

func sleepOnce() {
	time.Sleep(time.Nanosecond)
}

func sleepZero() {
	time.Sleep(0)
}

func sleepNegative() {
	time.Sleep(-time.Nanosecond)
}

func main() {
	sleepZero()
	sleepNegative()
	sleepOnce()
}
