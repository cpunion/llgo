package main

type sender interface{ Send(chan int, int) }
type value struct{ offset int }

func (v *value) Send(ch chan int, n int) { ch <- v.offset + n }

func deferred(s sender, ch chan int) { defer s.Send(ch, 2) }

func main() {
	var s sender = &value{40}
	ch := make(chan int, 1)
	go s.Send(ch, 1)
	if <-ch != 41 {
		panic("interface go receiver/argument mismatch")
	}
	deferred(s, ch)
	if <-ch != 42 {
		panic("interface defer receiver/argument mismatch")
	}
	s.Send(ch, 3)
	if <-ch != 43 {
		panic("interface call receiver/argument mismatch")
	}
	println("interface call/go/defer ok")
}
