//go:build !llgo || !windows || nogc || baremetal

package runtime

func EnterForeignThread() bool {
	return false
}

func ExitForeignThread(registered bool) {}

func releaseForeignThreadRegistration() {}
