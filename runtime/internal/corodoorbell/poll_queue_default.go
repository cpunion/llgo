//go:build !llgo || !darwin || baremetal

package corodoorbell

type nativePollQueue struct{}

func (*nativePollQueue) wait(first *PollFD, count uint32, timeoutMS int32) (int, int32) {
	return nativePollSet(first, count, timeoutMS)
}

func (*nativePollQueue) close() bool { return true }
