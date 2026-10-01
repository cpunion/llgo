package crc32_test

import (
	"testing"

	"golang.org/x/sys/cpu"
)

func TestAssemblyDispatchFeatures(t *testing.T) {
	if !cpu.X86.HasSSE42 || !cpu.X86.HasSSE41 || !cpu.X86.HasPCLMULQDQ {
		t.Fatal("CRC assembly oracle requires SSE4.2, SSE4.1 and PCLMULQDQ")
	}
	// VMOVQ in ieeeCLMULAvx512 is translated when the source file is compiled,
	// but it runs only if all three AVX512 flags are true. Do not infer runtime
	// AVX512 coverage from an ordinary CLMUL execution.
	avx512 := cpu.X86.HasAVX512F && cpu.X86.HasAVX512VL && cpu.X86.HasAVX512VPCLMULQDQ
	t.Logf("public IEEE dispatch: AVX512=%t (bulk >=1024), otherwise CLMUL (>=64)", avx512)
}
