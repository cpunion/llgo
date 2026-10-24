//go:build !llgo

package base_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/cmd/internal/base"
	cmdbuild "github.com/xgo-dev/llgo/cmd/internal/build"
	"github.com/xgo-dev/llgo/cmd/internal/clean"
	"github.com/xgo-dev/llgo/cmd/internal/install"
	"github.com/xgo-dev/llgo/cmd/internal/lldb"
	"github.com/xgo-dev/llgo/cmd/internal/monitor"
	"github.com/xgo-dev/llgo/cmd/internal/run"
	cmdtest "github.com/xgo-dev/llgo/cmd/internal/test"
)

func TestParseFlags(t *testing.T) {
	cmd := &base.Command{}
	cmd.Flag.Init("parse", flag.ContinueOnError)
	number := cmd.Flag.Int("number", 3, "a number")
	if !base.ParseFlags(cmd, []string{"-number=7", "payload"}) || *number != 7 ||
		!reflect.DeepEqual(cmd.Flag.Args(), []string{"payload"}) {
		t.Fatalf("number=%d, args=%v", *number, cmd.Flag.Args())
	}
}

func TestParseFlagsHelp(t *testing.T) {
	cmd := &base.Command{}
	cmd.Flag.Init("parse", flag.ContinueOnError)
	var output bytes.Buffer
	cmd.Flag.SetOutput(&output)
	if base.ParseFlags(cmd, []string{"-h"}) || !strings.Contains(output.String(), "Usage") {
		t.Fatalf("help did not stop the command: %q", output.String())
	}
}

var flagExitCommands = map[string]*base.Command{
	"build": cmdbuild.Cmd, "clean": clean.Cmd, "install": install.Cmd,
	"run": run.Cmd, "test": cmdtest.Cmd, "lldb": lldb.Cmd, "monitor": monitor.Cmd,
}

func TestCommandFlagParseExit(t *testing.T) {
	for name := range flagExitCommands {
		for _, help := range []bool{false, true} {
			label, args, code, text := "invalid", []string{"-unknown-command-flag"}, 2, "flag provided but not defined"
			if help {
				label, args, code, text = "help", []string{"-h"}, 0, "Usage"
			}
			t.Run(name+"/"+label, func(t *testing.T) {
				encoded, err := json.Marshal(struct {
					Name string
					Args []string
				}{name, args})
				if err != nil {
					t.Fatal(err)
				}
				command := exec.Command(os.Args[0], "-test.run=^TestCommandFlagParseExitHelper$")
				command.Env = append(os.Environ(), "LLGO_COMMAND_FLAG_EXIT="+string(encoded))
				output, err := command.CombinedOutput()
				actual := 0
				if err != nil {
					exit, ok := err.(*exec.ExitError)
					if !ok {
						t.Fatal(err)
					}
					actual = exit.ExitCode()
				}
				if actual != code || !strings.Contains(string(output), text) {
					t.Fatalf("exit=%d, want %d; output=%q", actual, code, output)
				}
			})
		}
	}
}

func TestCommandFlagParseExitHelper(t *testing.T) {
	encoded := os.Getenv("LLGO_COMMAND_FLAG_EXIT")
	if encoded == "" {
		return
	}
	var request struct {
		Name string
		Args []string
	}
	if err := json.Unmarshal([]byte(encoded), &request); err != nil {
		t.Fatal(err)
	}
	cmd := flagExitCommands[request.Name]
	if cmd == nil {
		t.Fatal("unknown helper command")
	}
	cmd.Run(cmd, request.Args)
	os.Exit(0)
}
