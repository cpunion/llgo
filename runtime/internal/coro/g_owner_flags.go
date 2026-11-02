package coro

// These independent owner-thread bits retain all combinations of the former
// destroyRoot, queued and waiting booleans. They share G's scalar prefix;
// allocation identity and every collector-visible pointer remain separate.
type gOwnerFlags uint8

const (
	gDestroysRoot gOwnerFlags = 1 << iota
	gQueued
	gWaiting
)

func (g *G) destroysRoot() bool { return g.ownerFlags&gDestroysRoot != 0 }
func (g *G) isQueued() bool     { return g.ownerFlags&gQueued != 0 }
func (g *G) isWaiting() bool    { return g.ownerFlags&gWaiting != 0 }

func (g *G) setDestroysRoot(value bool) {
	if value {
		g.ownerFlags |= gDestroysRoot
	} else {
		g.ownerFlags &^= gDestroysRoot
	}
}

func (g *G) setQueued(value bool) {
	if value {
		g.ownerFlags |= gQueued
	} else {
		g.ownerFlags &^= gQueued
	}
}

func (g *G) setWaiting(value bool) {
	if value {
		g.ownerFlags |= gWaiting
	} else {
		g.ownerFlags &^= gWaiting
	}
}
