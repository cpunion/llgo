package base

import (
	"flag"

	"github.com/xgo-dev/llgo/internal/mockable"
)

// ParseFlags reports whether the command should continue. Help prints usage
// and returns false; invalid flags exit with the command-line usage status.
func ParseFlags(cmd *Command, args []string) bool {
	if err := cmd.Flag.Parse(args); err != nil {
		if err != flag.ErrHelp {
			mockable.Exit(2)
		}
		return false
	}
	return true
}
