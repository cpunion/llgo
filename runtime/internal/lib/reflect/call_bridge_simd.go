//go:build llgo && goexperiment.simd && (amd64 || arm64)

package reflect

const useWasmReflectBridges = false
