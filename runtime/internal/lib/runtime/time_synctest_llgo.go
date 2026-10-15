//go:build go1.23 && llgo && llgo_coro && !coro_runtime_adapter_test && ((llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux)) || wasm || tinygo.wasm || baremetal || llgo_coro_host)

package runtime

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/coro"
	corort "github.com/xgo-dev/llgo/runtime/internal/runtime"
)

// Fake timers reuse the existing arg slot for optional state: enabling
// synctest does not enlarge every ordinary Timer or spawn timer-manager Gs.
type coroSynctestTimeTimer struct {
	state *coroTimeTimerState
	timer *corort.SynctestTimer
	arg   any
}

func coroSynctestTimeTimerOf(t *timeTimer) *coroSynctestTimeTimer {
	if !coro.ExecutionDomainEnabled() {
		return nil
	}
	fake, _ := t.state.arg.(*coroSynctestTimeTimer)
	return fake
}

func coroNewSynctestTimeTimer(t *timeTimer, when, period int64) {
	fake := &coroSynctestTimeTimer{state: t.state, arg: t.state.arg}
	var weakLive *uintptr
	if t.state.channel {
		weakLive = &t.state.weak.Live
	}
	fake.timer = corort.NewSynctestTimer(fake.fire, weakLive)
	t.state.arg = fake
	fake.reset(when, period, t.c)
}

func (fake *coroSynctestTimeTimer) fire(generation uint32, now, when int64) {
	state := fake.state
	if state.channel {
		coroTimerLock(&state.sendLock)
	}
	if !fake.timer.Current(generation) {
		if state.channel {
			coroTimerUnlock(&state.sendLock)
		}
		return
	}
	if state.afterFunc {
		// Publication creates a real, counted bubble child before the
		// controller may conclude quiescence again.
		go coroRunTimerCallback(fake.arg.(func()))
		return
	}
	delta := now - when
	if delta < 0 {
		delta = 0
	}
	coroTimerFire(state, fake.arg, generation, delta)
	if state.channel {
		coroTimerUnlock(&state.sendLock)
	}
}

func (fake *coroSynctestTimeTimer) stop(channel unsafe.Pointer) bool {
	state := fake.state
	if state.channel {
		coroTimerLock(&state.sendLock)
	}
	pending := fake.timer.Stop()
	if state.channel {
		if coroTimerDrainChannel(channel) {
			pending = true
		}
		coroTimerUnlock(&state.sendLock)
	}
	return pending
}

func (fake *coroSynctestTimeTimer) reset(when, period int64, channel unsafe.Pointer) bool {
	state := fake.state
	if state.channel {
		coroTimerLock(&state.sendLock)
	}
	pending, immediate, generation, now := fake.timer.Reset(when, period)
	if state.channel {
		if coroTimerDrainChannel(channel) {
			pending = true
		}
		coroTimerUnlock(&state.sendLock)
	}
	if immediate {
		fake.fire(generation, now, when)
	}
	return pending
}
