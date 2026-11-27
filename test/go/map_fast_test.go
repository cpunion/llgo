package gotest

import "testing"

type mapFastNamed32 uint32
type mapFastNamedString string

type mapFastStructKey struct {
	value uint64
}

func testMapFastPath[K comparable](t *testing.T, keys []K) {
	t.Helper()

	m := make(map[K]int)
	for i, key := range keys {
		m[key] = i + 1
	}
	for i, key := range keys {
		if got, ok := m[key]; !ok || got != i+1 {
			t.Fatalf("m[%v] = (%d, %v), want (%d, true)", key, got, ok, i+1)
		}
	}
	for _, key := range keys {
		delete(m, key)
	}
	if len(m) != 0 {
		t.Fatalf("len(m) = %d after delete, want 0", len(m))
	}
}

func TestMapFastPaths(t *testing.T) {
	a, b := 1, 2
	ch1, ch2 := make(chan int), make(chan int)

	testMapFastPath(t, []int32{-1, 0, 1, 1 << 30})
	testMapFastPath(t, []uint64{0, 1, 1 << 40, ^uint64(0)})
	testMapFastPath(t, []mapFastNamed32{0, 1, 1 << 30})
	testMapFastPath(t, []mapFastNamedString{"", "short", "named string"})
	testMapFastPath(t, []string{"", "short", "a string long enough to use the long-key lookup path"})
	testMapFastPath(t, []*int{nil, &a, &b})
	testMapFastPath(t, []chan int{nil, ch1, ch2})
	testMapFastPath(t, []mapFastStructKey{{0}, {1}, {1 << 40}})
}

func TestMapPrimaryDeleteEmptyRestCompaction(t *testing.T) {
	type ifaceKey struct{ id int }
	// Exercise single-bucket primary delete across all 8 slots in forward,
	// reverse, and split-hole orders (including interface keys with zero-size
	// values, matching context.cancelCtx.children).
	for count := 1; count <= 8; count++ {
		keys := make([]any, count)
		for i := range keys {
			keys[i] = &ifaceKey{id: i}
		}
		orders := [][]int{
			make([]int, count),
			make([]int, count),
			make([]int, count),
		}
		for i := 0; i < count; i++ {
			orders[0][i] = i               // forward: builds emptyOne chain then collapses on last delete
			orders[1][i] = count - 1 - i   // reverse: trailing slot every time
			orders[2][i] = (i*3 + 1) % count
		}
		// Deduplicate order 2 if count is divisible by 3.
		seen := make([]bool, count)
		idx := 0
		for i := 0; i < count; i++ {
			v := (i*3 + 1) % count
			if !seen[v] {
				seen[v] = true
				orders[2][idx] = v
				idx++
			}
		}
		for v := 0; v < count; v++ {
			if !seen[v] {
				orders[2][idx] = v
				idx++
			}
		}

		for oi, order := range orders {
			m := make(map[any]struct{}, 8)
			live := make([]bool, count)
			for i, k := range keys {
				m[k] = struct{}{}
				live[i] = true
			}
			for _, delIdx := range order {
				delete(m, keys[delIdx])
				live[delIdx] = false
				wantLen := 0
				for i, k := range keys {
					_, ok := m[k]
					if ok != live[i] {
						t.Fatalf("count=%d order=%d del=%d key=%d ok=%v want=%v", count, oi, delIdx, i, ok, live[i])
					}
					if live[i] {
						wantLen++
					}
				}
				if len(m) != wantLen {
					t.Fatalf("count=%d order=%d del=%d len=%d want=%d", count, oi, delIdx, len(m), wantLen)
				}
			}
			// Re-insert into the compacted bucket and verify lookup + delete.
			for i, k := range keys {
				m[k] = struct{}{}
				if _, ok := m[k]; !ok {
					t.Fatalf("reinsert count=%d order=%d key=%d missing", count, oi, i)
				}
			}
			if len(m) != count {
				t.Fatalf("reinsert count=%d order=%d len=%d want=%d", count, oi, len(m), count)
			}
			for _, k := range keys {
				delete(m, k)
			}
			if len(m) != 0 {
				t.Fatalf("final count=%d order=%d len=%d want=0", count, oi, len(m))
			}
		}
	}

	// Exercise buckets whose overflow chain becomes emptyRest after churn.
	m := make(map[any]int)
	const total = 128
	keys := make([]any, total)
	for i := 0; i < total; i++ {
		keys[i] = &ifaceKey{id: i}
	}
	for round := 0; round < 6; round++ {
		for i, k := range keys {
			m[k] = round*1000 + i
		}
		for i := 0; i < total; i++ {
			if got, ok := m[keys[i]]; !ok || got != round*1000+i {
				t.Fatalf("round=%d key=%d got=(%d,%v)", round, i, got, ok)
			}
		}
		for i := total - 1; i >= 2; i-- {
			delete(m, keys[i])
		}
		if len(m) != 2 {
			t.Fatalf("round=%d after shrink len=%d want=2", round, len(m))
		}
		for i := 0; i < total; i++ {
			got, ok := m[keys[i]]
			wantOK := i < 2
			if ok != wantOK || (wantOK && got != round*1000+i) {
				t.Fatalf("round=%d shrink check key=%d got=(%d,%v) wantOK=%v", round, i, got, ok, wantOK)
			}
		}
		// Update retained keys, add a few new keys, then delete all.
		m[keys[0]] = -1
		m[keys[5]] = 55
		if m[keys[0]] != -1 || m[keys[5]] != 55 {
			t.Fatalf("round=%d post-shrink update failed", round)
		}
		delete(m, keys[0])
		delete(m, keys[1])
		delete(m, keys[5])
		if len(m) != 0 {
			t.Fatalf("round=%d end len=%d want=0", round, len(m))
		}
	}
}
