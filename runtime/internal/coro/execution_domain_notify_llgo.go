//go:build llgo

package coro

import _ "unsafe"

//go:linkname executionDomainPostNotificationRaw C.__llgo_coro_domain_post_v1
func executionDomainPostNotificationRaw(sourceSlot, generation uint32) bool

func executionDomainPostNotification(id OperationID) bool {
	return executionDomainPostNotificationRaw(id.SourceSlot, id.Generation)
}
