package coro

import "testing"

func TestGOwnerFlagsPreserveIndependentBooleanTransitions(t *testing.T) {
	// Include every unused-bit pattern: no flag update may clear adjacent
	// scalar state or turn another ownership predicate on or off.
	for raw := 0; raw < 256; raw++ {
		for selected := 0; selected < 3; selected++ {
			for _, value := range []bool{false, true} {
				g := G{ownerFlags: gOwnerFlags(raw), taskState: taskStorageReleased,
					initialRunPrepared: true, yieldRunPrepared: true}
				before := g
				want := [3]bool{raw%2 == 1, raw/2%2 == 1, raw/4%2 == 1}
				want[selected] = value
				switch selected {
				case 0:
					g.setDestroysRoot(value)
				case 1:
					g.setQueued(value)
				case 2:
					g.setWaiting(value)
				}
				got := [3]bool{g.destroysRoot(), g.isQueued(), g.isWaiting()}
				if got != want || uint8(g.ownerFlags)/8 != uint8(before.ownerFlags)/8 {
					t.Fatalf("raw=%d selected=%d value=%t got=%v want=%v", raw, selected, value, got, want)
				}
				g.ownerFlags = before.ownerFlags
				if g != before {
					t.Fatalf("flag update changed adjacent G state: raw=%d selected=%d", raw, selected)
				}
			}
		}
	}
}
