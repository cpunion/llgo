package main

type stateFn func(*counter) stateFn

type counter struct {
	value int
	max   int
	state stateFn
}

func countState(c *counter) stateFn {
	c.value++
	println("count:", c.value)

	if c.value >= c.max {
		return nil
	}
	return countState
}

// DARWIN-ARM64: %[[NEXT_STATE:[0-9]+]] = call %main.stateFn %__llgo_funcval_code(ptr swiftself %[[STATE_ENV]], ptr %[[COUNTER_OBJ]])
// LINUX-AMD64: %[[NEXT_STATE:[0-9]+]] = call %main.stateFn %__llgo_funcval_code(ptr nest %[[STATE_ENV]], ptr %[[COUNTER_OBJ]])
func main() {
	c := &counter{max: 5, state: countState}

	for c.state != nil {
		c.state = c.state(c)
	}
}
