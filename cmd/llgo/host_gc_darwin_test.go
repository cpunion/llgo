//go:build !llgo

package main

import (
	"os"
	"os/exec"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
)

func TestHostGCDefault(t *testing.T) {
	const probe = "LLGO_TEST_HOST_GC_EXPECT"
	if expected, child := os.LookupEnv(probe); child {
		want, err := strconv.Atoi(expected)
		if err != nil {
			t.Fatal(err)
		}
		got := debug.SetGCPercent(100)
		debug.SetGCPercent(got)
		if got != want {
			t.Fatalf("host GC percent = %d, want %d", got, want)
		}
		return
	}
	var base []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOGC=") && !strings.HasPrefix(entry, probe+"=") {
			base = append(base, entry)
		}
	}
	for _, test := range []struct {
		name  string
		value string
		set   bool
		want  int
	}{
		{name: "default", want: 20},
		{name: "explicit", value: "100", set: true, want: 100},
		{name: "small", value: "5", set: true, want: 5},
		{name: "off", value: "off", set: true, want: -1},
		{name: "empty", set: true, want: 100},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestHostGCDefault$")
			cmd.Env = append(append([]string{}, base...), probe+"="+strconv.Itoa(test.want))
			if test.set {
				cmd.Env = append(cmd.Env, "GOGC="+test.value)
			}
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("CLI GC policy: %v\n%s", err, output)
			}
		})
	}
}
