//go:build !llgo || !windows || nogc || baremetal

package runtime

func releaseForeignThreadRegistration() {}
