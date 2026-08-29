//go:build (!darwin && !linux && !windows) || baremetal || tinygo.wasm

package atomic

func runtime_procPin() int {
	return 0
}

func runtime_procUnpin() {}
