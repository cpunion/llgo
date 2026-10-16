package libc

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestPicolibcIncludesRandomAndErrnoDependencies(t *testing.T) {
	base := filepath.FromSlash("/picolibc")
	config := GetPicolibcCompileConfig(base, "thumbv7m-unknown-unknown-eabi")
	for _, source := range []string{"stdlib/random.c", "stdlib/srandom.c", "errno/errno.c"} {
		if !slices.Contains(config.Groups[0].Files, filepath.Join(base, "newlib", "libc", filepath.FromSlash(source))) {
			t.Errorf("picolibc omitted dependency %s", source)
		}
	}
}
