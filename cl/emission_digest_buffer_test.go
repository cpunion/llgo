/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0.
 */

package cl

import (
	"crypto/sha256"
	"encoding/hex"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// Retain the original wire serializer as an independent compatibility oracle.
func legacyFramedEmissionKey(fields ...string) string {
	var out strings.Builder
	for _, field := range fields {
		out.WriteString(strconv.Itoa(len(field)))
		out.WriteByte(':')
		out.WriteString(field)
		out.WriteByte(';')
	}
	return out.String()
}

func TestFramedEmissionKeyPreservesOriginalBytes(t *testing.T) {
	cases := [][]string{nil, {""}, {"", ""}, {"12:x;y;", "\x00\xff汉字", "λ"}}
	for _, size := range []int{1, 9, 10, 99, 100, 999, 1000, 65536} {
		cases = append(cases, []string{"prefix", strings.Repeat("x", size), "suffix"})
	}
	cases = append(cases, make([]string, 2048))
	for _, fields := range cases {
		if got, want := framedEmissionKey(fields...), legacyFramedEmissionKey(fields...); got != want {
			t.Fatalf("framing changed for %d fields", len(fields))
		}
	}
}

func TestTypeGraphDigestBufferPreservesNestedFraming(t *testing.T) {
	d := &emissionTypeGraphDigester{}
	fields := [][]string{{strings.Repeat("large;:\x00", 8192)}, nil, {""}, {"node", "汉字", "0:;", "\x00"}, make([]string, 1024)}
	for _, schema := range []string{"", emissionStrictTypeGraphSchema, "schema\x00;:λ"} {
		d.schema = schema
		for _, values := range fields {
			text := legacyFramedEmissionKey(schema, legacyFramedEmissionKey(values...))
			sum := sha256.Sum256([]byte(text))
			if got, want := d.digestFields(values), hex.EncodeToString(sum[:]); got != want {
				t.Fatalf("nested framing changed for schema %q and %d fields", schema, len(values))
			}
		}
	}
}

// This is the pre-buffer algorithm, including root-local SCC numbering. It
// never calls the production framing or digestFields implementation.
func legacyEmissionGraphDigests(nodes []emissionTypeGraphNode, schema string, order []int) map[int]string {
	graph := newEmissionTypeGraphDigester(nodes, schema)
	digests := make(map[int]string)
	var digest func(int) string
	digest = func(root int) string {
		if value, ok := digests[root]; ok {
			return value
		}
		component := graph.component[root]
		var fields []string
		if !graph.cyclic[component] {
			fields = []string{"acyclic-node-v1", "node"}
			for _, token := range nodes[root].tokens {
				if token.edge {
					fields = append(fields, "edge", digest(token.child))
				} else {
					fields = append(fields, "text", token.text)
				}
			}
			fields = append(fields, "end-node")
		} else {
			fields = []string{"cyclic-component-v1"}
			local := make(map[int]int)
			var visit func(int)
			visit = func(node int) {
				id := len(local)
				local[node] = id
				fields = append(fields, "node", strconv.Itoa(id))
				for _, token := range nodes[node].tokens {
					if !token.edge {
						fields = append(fields, "text", token.text)
					} else if graph.component[token.child] != component {
						fields = append(fields, "external", digest(token.child))
					} else if target, ok := local[token.child]; ok {
						fields = append(fields, "ref", strconv.Itoa(target))
					} else {
						fields = append(fields, "define")
						visit(token.child)
					}
				}
				fields = append(fields, "end-node")
			}
			visit(root)
		}
		text := legacyFramedEmissionKey(schema, legacyFramedEmissionKey(fields...))
		sum := sha256.Sum256([]byte(text))
		value := hex.EncodeToString(sum[:])
		digests[root] = value
		return value
	}
	for _, root := range order {
		digest(root)
	}
	return digests
}

func TestTypeGraphDigestBufferMatchesLegacyAcrossOrderedGraphs(t *testing.T) {
	random := rand.New(rand.NewSource(0x6c6c676f))
	texts := []string{"", "node", "ref", ";:12", "\x00\xff", "汉字λ", strings.Repeat("wide", 128)}
	graphs := [][]emissionTypeGraphNode{
		{{tokens: []emissionTypeGraphToken{{text: "scalar"}}}},
		{{tokens: []emissionTypeGraphToken{{edge: true, child: 0}, {text: "self"}}}},
		{{tokens: []emissionTypeGraphToken{{edge: true, child: 1}, {edge: true, child: 1}}}, {tokens: []emissionTypeGraphToken{{edge: true, child: 0}, {edge: true, child: 2}}}, {tokens: []emissionTypeGraphToken{{text: "outside"}}}},
	}
	for sample := 0; sample < 160; sample++ {
		nodes := make([]emissionTypeGraphNode, 1+random.Intn(28))
		for node := range nodes {
			for count := random.Intn(9); count > 0; count-- {
				token := emissionTypeGraphToken{text: texts[random.Intn(len(texts))]}
				if random.Intn(2) == 0 {
					token = emissionTypeGraphToken{edge: true, child: random.Intn(len(nodes))}
				}
				nodes[node].tokens = append(nodes[node].tokens, token)
			}
		}
		graphs = append(graphs, nodes)
	}
	schemas := []string{emissionGoLinknameTypeGraphSchema, emissionStrictTypeGraphSchema, emissionStrictABITypeGraphSchema, emissionIdentityFreeTypeGraphSchema, emissionCFunctionTypeGraphSchema}
	for index, nodes := range graphs {
		order := random.Perm(len(nodes))
		for _, schema := range schemas {
			want := legacyEmissionGraphDigests(nodes, schema, order)
			current := newEmissionTypeGraphDigester(nodes, schema)
			for _, root := range order {
				if got := current.digestNode(root); got != want[root] {
					t.Fatalf("graph %d root %d schema %s changed", index, root, schema)
				}
			}
			// Reindex the graph without altering its ordered topology. Neither SCC
			// numbering nor buffer history may become part of the structural identity.
			ids := random.Perm(len(nodes))
			renamed := make([]emissionTypeGraphNode, len(nodes))
			for old, node := range nodes {
				tokens := append([]emissionTypeGraphToken(nil), node.tokens...)
				for i := range tokens {
					if tokens[i].edge {
						tokens[i].child = ids[tokens[i].child]
					}
				}
				renamed[ids[old]].tokens = tokens
			}
			other := newEmissionTypeGraphDigester(renamed, schema)
			for _, root := range order {
				if got := other.digestNode(ids[root]); got != want[root] {
					t.Fatalf("reindex changed graph %d root %d", index, root)
				}
				if got := current.digestNode(root); got != want[root] {
					t.Fatal("later reuse mutated an earlier cached digest")
				}
			}
		}
	}
}
