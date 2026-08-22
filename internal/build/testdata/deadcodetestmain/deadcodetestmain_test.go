package deadcodetestmain

import "testing"

func TestReachable(t *testing.T) {
	if got := Answer(); got != 42 {
		t.Fatalf("Answer() = %d, want 42", got)
	}
}
