package ssa

import "github.com/xgo-dev/llgo/internal/callerlocation"

// EmitLocationRecord emits an idempotent runtime source-location update unless
// the current block already has the same update and debug position with no
// intervening call. emit must append that update to the current block.
// Non-tail insertion keeps all records; it cannot reuse append-only history.
func (b Builder) EmitLocationRecord(kind, name, file string, line int, emit func()) {
	if !b.locationRecordsAtEnd {
		emit()
		return
	}
	block := b.impl.GetInsertBlock()
	location := callerlocation.Location{Kind: kind, Function: name, File: file, Line: line}
	if b.locationRecords.Repeated(block, block.LastInstruction(), location, b.diLocation) {
		return
	}
	emit()
	b.locationRecords.Checkpoint(block.LastInstruction())
}
