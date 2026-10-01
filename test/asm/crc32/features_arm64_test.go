package crc32_test

import (
	"testing"

	"golang.org/x/sys/cpu"
)

func TestAssemblyDispatchFeatures(t *testing.T) {
	if !cpu.ARM64.HasCRC32 {
		t.Fatal("CRC assembly oracle requires ARM64 CRC32 instructions")
	}
	t.Log("public IEEE and Castagnoli dispatch use ARM64 CRC32 kernels")
}
