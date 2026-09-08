/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package tinygogc

const (
	defaultGCPercent = int32(100)
	minimumGCHeap    = uint64(4 << 20)
	// This collector is synchronous. Guarantee allocation progress even at
	// GOGC=0 instead of rescanning all roots for every small allocation. This
	// is a backend policy, not Go's concurrent collector's pacing algorithm.
	minimumGCProgress = uint64(64 << 10)
	disabledGCGoal    = ^uint64(0)
)

type gcPacing struct {
	initialized bool
	percent     int32
	live        uint64
	goal        uint64
}

func (p *gcPacing) init(percent int32, live uint64) {
	if !p.initialized {
		p.live = live
		p.setPercent(percent, live)
	}
}

func (p *gcPacing) setPercent(percent int32, live uint64) int32 {
	old := defaultGCPercent
	if p.initialized {
		old = p.percent
	} else {
		p.initialized = true
		p.live = live
	}
	if percent < 0 {
		percent = -1
	}
	p.percent = percent
	p.goal = gcHeapGoal(p.live, percent)
	return old
}

func (p *gcPacing) collected(live uint64) {
	if p.initialized {
		p.live = live
		p.goal = gcHeapGoal(live, p.percent)
	}
}

func (p *gcPacing) automatic() bool { return p.initialized && p.percent >= 0 }

func (p *gcPacing) nextGC() uint64 {
	if !p.initialized {
		return disabledGCGoal
	}
	return p.goal
}

func (p *gcPacing) shouldCollect(live, allocation uint64) bool {
	return p.automatic() && (live >= p.goal || allocation >= p.goal-live)
}

func gcHeapGoal(live uint64, percent int32) uint64 {
	if percent < 0 {
		return disabledGCGoal
	}
	progress := gcPercentOf(live, uint64(percent))
	if progress < minimumGCProgress {
		progress = minimumGCProgress
	}
	goal := gcSaturatingAdd(live, progress)
	if minimum := gcPercentOf(minimumGCHeap, uint64(percent)); goal < minimum {
		goal = minimum
	}
	return goal
}

func gcPercentOf(value, percent uint64) uint64 {
	whole := value / 100
	if percent != 0 && whole > disabledGCGoal/percent {
		return disabledGCGoal
	}
	// percent is a nonnegative int32, so the remainder product fits uint64.
	return gcSaturatingAdd(whole*percent, value%100*percent/100)
}

func gcSaturatingAdd(a, b uint64) uint64 {
	if a > disabledGCGoal-b {
		return disabledGCGoal
	}
	return a + b
}

// ParseGCPercent accepts the same decimal int32 syntax as Go's GOGC setting,
// without importing strconv into the allocation/runtime bootstrap path.
func ParseGCPercent(value string) int32 {
	if value == "off" {
		return -1
	}
	if value == "" {
		return defaultGCPercent
	}
	negative := value[0] == '-'
	if negative || value[0] == '+' {
		value = value[1:]
	}
	if value == "" {
		return defaultGCPercent
	}
	limit := uint32(1<<31 - 1)
	if negative {
		limit++
	}
	var n uint32
	for i := 0; i < len(value); i++ {
		digit := uint32(value[i] - '0')
		if digit > 9 || n > (limit-digit)/10 {
			return defaultGCPercent
		}
		n = n*10 + digit
	}
	if negative && n != 0 {
		return -1
	}
	return int32(n)
}
