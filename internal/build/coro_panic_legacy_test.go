//go:build !llgo

package build

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
)

// This is an integration regression for the real runtime closure, not a probe.
// It keeps the legacy ABI fail-closed at the first user-code-capable terminal
// panic edge. If Rethrow's terminal-unhandled branch is later split behind a
// different panic ABI adapter, this expected chain must be deliberately
// replaced by the new adapter's certificate test.
func TestRealRuntimeLegacyPanicPlainCertificateStopsAtDynamicError(t *testing.T) {
	sentinel := errors.New("legacy panic blocker verified")
	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	conf.EnableCoroEntryResolution = true
	conf.EnableCoroPhysicalABI = true
	conf.EnableCoroChildAwait = true
	conf.EnableCoroPlainDispatch = true
	conf.EnableCoroProgramBootstrapABI = true
	conf.EnableCoroProgramBootstrapRun = true
	conf.CoroPlanBuilder = func(input CoroPlanInput) (*coro.SSAPlan, error) {
		plan, err := input.Analyze(nil, coro.SSAConfig{MaxPlainInstructions: -1})
		if err != nil {
			return nil, err
		}
		err = validateCoroUnwindOnlyLoweredCalls(plan, coro.PanicLegacyABIV0)
		if err == nil {
			return nil, fmt.Errorf("real runtime legacy panic closure unexpectedly received a plain certificate")
		}
		message := err.Error()
		cursor := 0
		for _, part := range []string{
			"runtime.Panic[",
			"runtime.Rethrow[",
			"runtime.TracePanic[",
			"runtime.printany[",
			"dynamic invoke Error",
		} {
			index := strings.Index(message[cursor:], part)
			if index < 0 {
				return nil, fmt.Errorf("real runtime legacy panic blocker %q lacks ordered path component %q", message, part)
			}
			cursor += index + len(part)
		}
		return nil, sentinel
	}
	_, err := Do([]string{"../../cl/_testgo/print"}, conf)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Do error = %v, want verified legacy panic blocker", err)
	}
}
