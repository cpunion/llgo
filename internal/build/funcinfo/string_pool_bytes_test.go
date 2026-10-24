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

import (
	"bytes"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// previousStringPool preserves the former encoding algorithm as an independent
// compatibility oracle. Interior, suffix and embedded-NUL matches keep the
// same first byte offset even when inputs are outside ordinary symbol text.
type previousStringPool struct {
	offsets map[string]uint32
	data    []byte
	text    string
}

func (p *previousStringPool) offset(s string) (uint32, error) {
	if o, ok := p.offsets[s]; ok {
		return o, nil
	}
	if o := strings.Index(p.text, s+"\x00"); o >= 0 {
		p.offsets[s] = uint32(o)
		return uint32(o), nil
	}
	if len(p.data)+len(s)+1 > math.MaxUint32 {
		return 0, fmt.Errorf("oversized pool")
	}
	o := uint32(len(p.data))
	p.data = append(p.data, s...)
	p.data = append(p.data, 0)
	p.text = string(p.data)
	p.offsets[s] = o
	return o, nil
}
func TestStringPoolPreservesPreviousByteEncoding(t *testing.T) {
	input := []string{"", "alpha/beta/gamma.go", "gamma.go", "beta/gamma.go", "gamma", "go", "alpha/beta/gamma.go", "x\x00y", "y", "\x00y", "\xff\x80tail", "tail", "ail"}
	random := rand.New(rand.NewSource(149882))
	for i := 0; i < 1200; i++ {
		n := random.Intn(48)
		b := make([]byte, n)
		for j := range b {
			b[j] = byte(random.Intn(256))
		}
		input = append(input, string(b))
		if len(b) > 0 {
			input = append(input, string(b[random.Intn(len(b)):]))
		}
	}
	old := previousStringPool{offsets: map[string]uint32{"": 0}, data: []byte{0}, text: "\x00"}
	next := stringPool{offsets: map[string]uint32{"": 0}, data: []byte{0}}
	for i, s := range input {
		a, ea := old.offset(s)
		b, eb := next.offset(s)
		if ea != nil || eb != nil || a != b || !bytes.Equal(old.data, next.data) {
			t.Fatalf("input %d %q: old=%d/%v new=%d/%v", i, s, a, ea, b, eb)
		}
	}
}
func BenchmarkStringPoolLarge(b *testing.B) {
	values := make([]string, 4096)
	for i := range values {
		values[i] = fmt.Sprintf("receiver_%05d.(*LongInstantiatedType[map[string][]uint64]).Method$coro.resume", i)
	}
	b.Run("previous", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			p := previousStringPool{offsets: map[string]uint32{"": 0}, data: []byte{0}, text: "\x00"}
			for _, s := range values {
				if _, err := p.offset(s); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
	b.Run("indexed", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			p := stringPool{offsets: map[string]uint32{"": 0}, data: []byte{0}}
			for _, s := range values {
				if _, err := p.offset(s); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}

// The compressed tree must preserve the old byte encoding for all small binary
// names, regardless of insertion order or NUL-delimited substring reuse.
func TestStringPoolBranchingAndStringTableCompatibility(t *testing.T) {
	values := []string{"", "abc", "xabc", "bc", "ybc", "ab", "a", "λ/中文.go", "中文.go"}
	var extend func([]byte, int)
	extend = func(prefix []byte, remaining int) {
		values = append(values, string(prefix))
		if remaining == 0 {
			return
		}
		for _, c := range []byte{0, 'a', 'b', 0xff} {
			extend(append(append([]byte(nil), prefix...), c), remaining-1)
		}
	}
	extend(nil, 4)
	for seed := int64(0); seed < 4; seed++ {
		input := append([]string(nil), values...)
		rand.New(rand.NewSource(seed)).Shuffle(len(input), func(i, j int) { input[i], input[j] = input[j], input[i] })
		old := previousStringPool{offsets: map[string]uint32{"": 0}, data: []byte{0}, text: "\x00"}
		next := stringPool{offsets: map[string]uint32{"": 0}, data: []byte{0}}
		for i, s := range input {
			a, ea := old.offset(s)
			b, eb := next.offset(s)
			if ea != nil || eb != nil || a != b || !bytes.Equal(old.data, next.data) {
				t.Fatalf("seed %d input %d %q: old=%d/%v new=%d/%v", seed, i, s, a, ea, b, eb)
			}
		}
		ids, offsets, data, err := buildStringTable(input)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs, ordered, err := assignStringIDs(input)
		if err != nil {
			t.Fatal(err)
		}
		oracle := previousStringPool{offsets: map[string]uint32{"": 0}, data: []byte{0}, text: "\x00"}
		wantOffsets := make([]uint32, len(ordered))
		for i, s := range ordered {
			wantOffsets[i], err = oracle.offset(s)
			if err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(ids, wantIDs) || !reflect.DeepEqual(offsets, wantOffsets) || !bytes.Equal(data, oracle.data) {
			t.Fatalf("seed %d: encoded string table changed", seed)
		}
	}
}
