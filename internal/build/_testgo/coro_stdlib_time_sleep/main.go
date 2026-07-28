package main

import "time"

func main() {
	verifyJSHost()

	start := time.Now()
	if start.Unix() < 1_000_000_000 {
		panic("time.Now returned an invalid wall clock")
	}
	_, offset := start.Zone()
	if offset < -24*60*60 || offset > 24*60*60 {
		panic("time.Local returned an invalid UTC offset")
	}
	time.Sleep(200 * time.Millisecond)
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		panic("time.Sleep returned before its monotonic deadline")
	}
}
