package build

import (
	"strings"

	"github.com/xgo-dev/llgo/internal/quoted"
)

func withoutRuntimeModFileFlags(flags []string) []string {
	var out []string
	for i := 0; i < len(flags); i++ {
		if strings.HasPrefix(flags[i], "-modfile=") {
			continue
		}
		if flags[i] == "-modfile" && i+1 < len(flags) && !strings.HasPrefix(flags[i+1], "-") {
			i++
			continue
		}
		// Keep a malformed flag so cmd/go still reports its original error.
		out = append(out, flags[i])
	}
	return out
}

func withoutRuntimeModFileEnv(value string) string {
	fields, err := quoted.Split(value)
	if err != nil {
		// Do not repair invalid command quoting while selecting a runtime.
		return value
	}
	var out []string
	remaining := value
	for _, field := range fields {
		remaining = strings.TrimLeft(remaining, " \t\r\n")
		n := len(field)
		if remaining[0] == '\'' || remaining[0] == '"' {
			n += 2
		}
		raw := remaining[:n]
		remaining = remaining[n:]
		if !strings.HasPrefix(field, "-modfile=") {
			// Preserve each retained field's original quoting: GOFLAGS uses
			// cmd/go's quoting rules, not shell or JSON escaping.
			out = append(out, raw)
		}
	}
	return strings.Join(out, " ")
}
