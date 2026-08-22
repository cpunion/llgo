package deadcodetestmain

import "testing"

func TestReachable(t *testing.T) {
	if got := InitializedAnswer(); got != 7 {
		t.Fatalf("InitializedAnswer() = %d, want 7", got)
	}
	if got := Answer(); got != 42 {
		t.Fatalf("Answer() = %d, want 42", got)
	}
	if DeadType() == nil {
		t.Fatal("DeadType() returned nil")
	}
}
