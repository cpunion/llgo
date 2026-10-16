//go:build !llgo

package targets

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBaremetalCoroStartupAndGCLayoutSelection(t *testing.T) {
	root := filepath.Join("..", "..", "targets")
	for _, target := range []string{"cortex-m-qemu", "mps2-an385-qemu"} {
		config, err := NewResolver(root).Resolve(target)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range []string{"targets/cortex-m-qemu.s", "targets/device/arm/cortex-m-start.c"} {
			if !slices.Contains(config.ExtraFiles, file) {
				t.Errorf("%s omitted startup input %s", target, file)
			}
		}
	}
	for _, script := range []string{"arm.ld", "riscv.ld"} {
		contents, err := os.ReadFile(filepath.Join(root, script))
		if err != nil {
			t.Fatal(err)
		}
		for _, symbol := range []string{"_heapStart = _heap_start;", "_heapEnd = _heap_end;", "_stack_end = .;"} {
			if !strings.Contains(string(contents), symbol) {
				t.Errorf("%s does not bind GC memory layout %s", script, symbol)
			}
		}
	}
}

func TestBaremetalQEMUMemoryBudgetsRemainDistinct(t *testing.T) {
	root := filepath.Join("..", "..", "targets")
	for script, sizes := range map[string][]string{
		"lm3s6965.ld":   {"LENGTH = 256K", "LENGTH = 64K"},
		"mps2-an385.ld": {"LENGTH = 4M", "_stack_size = 16K;"},
	} {
		contents, err := os.ReadFile(filepath.Join(root, script))
		if err != nil {
			t.Fatal(err)
		}
		for _, size := range sizes {
			if !strings.Contains(string(contents), size) {
				t.Errorf("%s lost its explicit resource budget %s", script, size)
			}
		}
	}
}
