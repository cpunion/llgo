package runtime

import (
	"unsafe"

	corort "github.com/xgo-dev/llgo/runtime/internal/runtime"
)

//llgo:managedlink
//go:linkname synctest_run internal/synctest.Run
func synctest_run(f func()) {
	corort.SynctestRun(f)
}

//llgo:managedlink
//go:linkname synctest_wait internal/synctest.Wait
func synctest_wait() { corort.SynctestWait() }

//llgo:managedlink
//go:linkname llgoSynctestSleep runtime.llgoSynctestSleep
func llgoSynctestSleep(delay int64) bool { return corort.SynctestSleep(delay) }

//go:linkname synctest_isInBubble internal/synctest.IsInBubble
func synctest_isInBubble() bool {
	return corort.SynctestIsInBubble()
}

//go:linkname synctest_associate internal/synctest.associate
func synctest_associate(p unsafe.Pointer) int {
	return synctestObjectAssociation(p, true)
}

//go:linkname synctest_disassociate internal/synctest.disassociate
func synctest_disassociate(p unsafe.Pointer) { synctestForgetObject(p) }

//go:linkname synctest_isAssociated internal/synctest.isAssociated
func synctest_isAssociated(p unsafe.Pointer) bool {
	return synctestObjectAssociation(p, false) == 1
}

//go:linkname synctest_acquire internal/synctest.acquire
func synctest_acquire() any {
	return corort.SynctestAcquire()
}

//go:linkname synctest_release internal/synctest.release
func synctest_release(bubble any) {
	corort.SynctestRelease(bubble)
}

//llgo:managedlink
//go:linkname synctest_inBubble internal/synctest.inBubble
func synctest_inBubble(bubble any, f func()) {
	corort.SynctestInBubble(bubble, f)
}
