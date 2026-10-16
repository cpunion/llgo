//go:build !llgo

package coro

// Host protocol tests consume returned notifications directly. No target
// executor is installed by an ordinary Go test; never pretend a wake was sent.
func executionDomainPostNotification(id OperationID) bool { return false }
