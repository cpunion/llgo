//go:build !llgo

package build

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildFlagParseExit(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		code int
		text string
	}{
		{"unknown", []string{"-unknown-build-flag"}, 2, "flag provided but not defined"},
		{"invalid optimization", []string{"-O=invalid"}, 2, "invalid value"},
		{"help", []string{"-h"}, 0, "Usage:"},
	} {
		t.Run(test.name, func(t *testing.T) {
			image := filepath.Join(t.TempDir(), "must-not-build")
			args := append([]string{"-o", image}, test.args...)
			encoded, err := json.Marshal(args)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestBuildFlagParseExitHelper$")
			cmd.Env = append(os.Environ(), "LLGO_BUILD_FLAG_EXIT_ARGS="+string(encoded))
			output, err := cmd.CombinedOutput()
			code := 0
			if err != nil {
				exit, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatal(err)
				}
				code = exit.ExitCode()
			}
			if code != test.code || !strings.Contains(string(output), test.text) {
				t.Fatalf("exit=%d, want %d; output=%q", code, test.code, output)
			}
			if _, err := os.Stat(image); !os.IsNotExist(err) {
				t.Fatalf("flag failure/help produced an output: %v", err)
			}
		})
	}
}

func TestBuildFlagParseExitHelper(t *testing.T) {
	encoded := os.Getenv("LLGO_BUILD_FLAG_EXIT_ARGS")
	if encoded == "" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(encoded), &args); err != nil {
		t.Fatal(err)
	}
	runCmd(Cmd, args)
	os.Exit(0)
}
