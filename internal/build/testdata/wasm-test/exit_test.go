package wasmtest

import (
	"flag"
	"testing"
	"time"
)

var intentionalHostExitFailure = flag.Bool("llgo.intentional-exit-failure", false, "exercise failed-test process status")

// The host CI driver opts into this failure and requires a nonzero process
// status. A host wait first exercises exit after Asyncify has been active.
func TestIntentionalHostExitFailure(t *testing.T) {
	if !*intentionalHostExitFailure {
		return
	}
	time.Sleep(10 * time.Millisecond)
	t.Fatal("intentional wasm exit-status probe")
}
