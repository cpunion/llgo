package wasmtest

import (
	"os"
	"testing"
	"time"
)

// The host CI driver opts into this failure and requires a nonzero process
// status. A host wait first exercises exit after Asyncify has been active.
func TestIntentionalHostExitFailure(t *testing.T) {
	if os.Getenv("LLGO_WASM_TEST_FAILURE") != "1" {
		return
	}
	time.Sleep(10 * time.Millisecond)
	t.Fatal("intentional wasm exit-status probe")
}
