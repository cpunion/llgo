package wasmevent

type dispatcher struct {
	generation uintptr
	pending    uintptr
}

func (d *dispatcher) schedule() uintptr {
	d.generation++
	if d.generation == 0 {
		d.generation++
	}
	d.pending = d.generation
	return d.pending
}

func (d *dispatcher) consume(generation uintptr) bool {
	if generation == 0 || generation != d.pending {
		return false
	}
	d.pending = 0
	return true
}

func (d *dispatcher) cancel() {
	d.pending = 0
}
