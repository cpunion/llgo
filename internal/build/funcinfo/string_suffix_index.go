/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package funcinfo

// Every NUL-free pool match ends at a C-string terminator. Reversed byte
// prefixes therefore index precisely the suffixes that the pool may reuse.
// Compressed edges borrow existing input strings; each insertion creates at
// most a leaf and a branch. A node keeps the earliest terminator offset below
// its edge, so queries ending inside an edge also return the first match.
type stringSuffixIndex struct{ root stringSuffixNode }
type stringSuffixNode struct {
	text     string
	lo, hi   int
	firstEnd uint32
	children map[byte]*stringSuffixNode
}

func (index *stringSuffixIndex) find(s string) (uint32, bool) {
	parent := &index.root
	remaining := len(s)
	for remaining > 0 {
		child := parent.children[s[remaining-1]]
		if child == nil {
			return 0, false
		}
		n := child.hi - child.lo
		if remaining < n {
			n = remaining
		}
		for i := 0; i < n; i++ {
			if s[remaining-1-i] != child.text[child.hi-1-i] {
				return 0, false
			}
		}
		if n == remaining {
			return child.firstEnd - uint32(len(s)), true
		}
		remaining -= n
		parent = child
	}
	return 0, false
}

func (index *stringSuffixIndex) insert(s string, end uint32) {
	if len(s) == 0 {
		return
	}
	parent := &index.root
	remaining := len(s)
	for {
		key := s[remaining-1]
		child := parent.children[key]
		if child == nil {
			if parent.children == nil {
				parent.children = make(map[byte]*stringSuffixNode)
			}
			parent.children[key] = &stringSuffixNode{text: s, hi: remaining, firstEnd: end}
			return
		}
		n := child.hi - child.lo
		if remaining < n {
			n = remaining
		}
		shared := 0
		for shared < n && s[remaining-1-shared] == child.text[child.hi-1-shared] {
			shared++
		}
		// An existing edge already represents every suffix of this segment,
		// and its terminator precedes the newly appended segment's terminator.
		if shared == remaining {
			return
		}
		if shared == child.hi-child.lo {
			remaining -= shared
			parent = child
			continue
		}
		branch := &stringSuffixNode{text: child.text, lo: child.hi - shared, hi: child.hi, firstEnd: child.firstEnd, children: make(map[byte]*stringSuffixNode)}
		child.hi -= shared
		branch.children[child.text[child.hi-1]] = child
		remaining -= shared
		branch.children[s[remaining-1]] = &stringSuffixNode{text: s, hi: remaining, firstEnd: end}
		parent.children[key] = branch
		return
	}
}
