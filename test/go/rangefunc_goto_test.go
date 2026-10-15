package gotest

import "testing"

func rangeLabelValues(yield func(int) bool) {
	for i := 1; i <= 2; i++ {
		if !yield(i) {
			return
		}
	}
}

func TestRangeFunctionGotoRestartsLabeledIterator(t *testing.T) {
	iterations, calls := 0, 0
	values := func(yield func(int) bool) {
		calls++
		rangeLabelValues(yield)
	}
	for range rangeLabelValues {
	Again:
		for value := range values {
			iterations++
			if value != 1 {
				t.Fatal("goto continued the current iterator instead of restarting it")
			}
			if iterations == 4 {
				if calls != 4 {
					t.Fatalf("iterator called %d times, want 4", calls)
				}
				return
			}
			goto Again
		}
	}
	t.Fatal("iterator completed instead of restarting")
}

func TestRangeFunctionLabelContinueAndBreak(t *testing.T) {
	calls, iterations, sum := 0, 0, 0
	values := func(yield func(int) bool) {
		calls++
		rangeLabelValues(yield)
	}
Continue:
	for value := range values {
		iterations++
		sum += value
		continue Continue
	}
	if calls != 1 || iterations != 2 || sum != 3 {
		t.Fatalf("continue changed iterator lifetime: calls=%d iterations=%d sum=%d", calls, iterations, sum)
	}
Break:
	for range values {
		iterations++
		break Break
	}
	if calls != 2 || iterations != 3 {
		t.Fatalf("break changed iterator lifetime: calls=%d iterations=%d", calls, iterations)
	}
}
