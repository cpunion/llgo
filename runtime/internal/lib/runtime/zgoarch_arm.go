//go:build arm && !tinygo.wasm

package runtime

// Freestanding ARM targets use the same Go architecture facts as native ARM.
const (
	GOARCH    = "arm"
	IsPpc64   = 0
	IsPpc64le = 0
)
