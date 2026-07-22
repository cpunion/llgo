package runtime

func NumCPU() int {
	return int(c_maxprocs())
}

func Breakpoint() {
	c_debugtrap()
}

func Gosched() {
	coroSchedulerYield()
}

func NumCgoCall() int64 {
	return 0
}
