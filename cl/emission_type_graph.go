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

package cl

import (
	"strconv"

	llssa "github.com/goplus/llgo/ssa"
	"go/types"
)

const (
	emissionGoLinknameTypeGraphSchema    = "llgo.emission.go-linkname-type-graph.v1"
	emissionGoLinknameTypeGraphKeyPrefix = "graph-sha256-v1:"
)

// emissionTypeGraphToken retains the exact ordered scalar/child sequence of
// one structural ABI node. Child indexes never enter the final digest
// directly: acyclic children contribute their Merkle digest, while edges
// inside a recursive SCC use deterministic root-local graph indexes.
type emissionTypeGraphToken struct {
	text  string
	child int
	edge  bool
}

type emissionTypeGraphNode struct {
	tokens []emissionTypeGraphToken
}

// emissionTypeGraphBuilder turns the reachable go/types value graph into a
// compact intermediate graph. Named identity is deliberately transparent for
// go:linkname ABI pairing: a named value and its physical anonymous
// underlying type share one node. Pointer sharing outside recursive SCCs is
// not semantic and is removed later by Merkle hashing.
type emissionTypeGraphBuilder struct {
	ids   map[types.Type]int
	nodes []emissionTypeGraphNode
}

func compactStructuralGoLinknameABITypeKey(typ types.Type) string {
	if signature, ok := types.Unalias(typ).(*types.Signature); ok {
		typ = normalizeGoLinknameABISignature(signature)
	}
	builder := emissionTypeGraphBuilder{ids: make(map[types.Type]int)}
	root := builder.node(typ)
	digester := newEmissionTypeGraphDigester(builder.nodes)
	return emissionGoLinknameTypeGraphKeyPrefix + digester.digestNode(root)
}

func (b *emissionTypeGraphBuilder) node(typ types.Type) int {
	if typ == nil {
		if index, ok := b.ids[nil]; ok {
			return index
		}
		return b.reserveAndFill(nil, nil)
	}
	typ = types.Unalias(typ)
	if named, ok := typ.(*types.Named); ok {
		if index, exists := b.ids[named]; exists {
			return index
		}
		underlying := types.Unalias(named.Underlying())
		if index, exists := b.ids[underlying]; exists {
			b.ids[named] = index
			return index
		}
		return b.reserveAndFill(named, underlying)
	}
	if index, ok := b.ids[typ]; ok {
		return index
	}
	return b.reserveAndFill(typ, typ)
}

func (b *emissionTypeGraphBuilder) reserveAndFill(identity, body types.Type) int {
	index := len(b.nodes)
	b.nodes = append(b.nodes, emissionTypeGraphNode{})
	b.ids[identity] = index
	if named, ok := identity.(*types.Named); ok {
		b.ids[types.Unalias(named.Underlying())] = index
	}
	b.nodes[index] = b.describe(body)
	return index
}

func (b *emissionTypeGraphBuilder) describe(typ types.Type) emissionTypeGraphNode {
	var node emissionTypeGraphNode
	text := func(values ...string) {
		for _, value := range values {
			node.tokens = append(node.tokens, emissionTypeGraphToken{text: value})
		}
	}
	edge := func(child types.Type) {
		node.tokens = append(node.tokens, emissionTypeGraphToken{child: b.node(child), edge: true})
	}
	pkgKey := func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		return llssa.PathOf(pkg)
	}
	if typ == nil {
		text("nil-type")
		return node
	}
	switch typ := typ.(type) {
	case *types.Basic:
		text("basic", strconv.Itoa(int(typ.Kind())), typ.Name())
	case *types.Pointer:
		text("pointer")
		edge(typ.Elem())
	case *types.Array:
		text("array", strconv.FormatInt(typ.Len(), 10))
		edge(typ.Elem())
	case *types.Slice:
		text("slice")
		edge(typ.Elem())
	case *types.Map:
		text("map")
		edge(typ.Key())
		edge(typ.Elem())
	case *types.Chan:
		text("chan", strconv.Itoa(int(typ.Dir())))
		edge(typ.Elem())
	case *types.Named:
		// node removes named wrappers before describe. Keep this defensive case
		// fail-closed and deterministic if go/types ever exposes a named
		// underlying node through a new representation.
		text("named-underlying")
		edge(typ.Underlying())
	case *types.Struct:
		text("struct", strconv.Itoa(typ.NumFields()))
		for index := 0; index < typ.NumFields(); index++ {
			edge(typ.Field(index).Type())
		}
	case *types.Tuple:
		text("tuple", strconv.Itoa(typ.Len()))
		for index := 0; index < typ.Len(); index++ {
			edge(typ.At(index).Type())
		}
	case *types.Signature:
		text("signature", strconv.FormatBool(typ.Variadic()))
		if typ.Recv() != nil {
			text("recv")
			edge(typ.Recv().Type())
		}
		for _, parameters := range []*types.TypeParamList{typ.RecvTypeParams(), typ.TypeParams()} {
			text("type-params")
			if parameters != nil {
				for index := 0; index < parameters.Len(); index++ {
					edge(parameters.At(index))
				}
			}
		}
		edge(typ.Params())
		edge(typ.Results())
	case *types.Interface:
		typ.Complete()
		text("interface", strconv.Itoa(typ.NumMethods()), strconv.Itoa(typ.NumEmbeddeds()))
		for index := 0; index < typ.NumMethods(); index++ {
			method := typ.Method(index)
			text(pkgKey(method.Pkg()), method.Name())
			edge(method.Type())
		}
		for index := 0; index < typ.NumEmbeddeds(); index++ {
			edge(typ.EmbeddedType(index))
		}
	case *types.TypeParam:
		object := typ.Obj()
		name, pkg := "", ""
		if object != nil {
			name, pkg = object.Name(), pkgKey(object.Pkg())
		}
		text("type-param", pkg, name)
		edge(typ.Constraint())
	case *types.Union:
		text("union", strconv.Itoa(typ.Len()))
		for index := 0; index < typ.Len(); index++ {
			term := typ.Term(index)
			text(strconv.FormatBool(term.Tilde()))
			edge(term.Type())
		}
	default:
		text("other-type", types.TypeString(typ, func(pkg *types.Package) string { return pkgKey(pkg) }))
	}
	return node
}

type emissionTypeGraphDigester struct {
	nodes      []emissionTypeGraphNode
	component  []int
	components [][]int
	cyclic     []bool
	digests    map[int]string
	active     map[int]bool
}

func newEmissionTypeGraphDigester(nodes []emissionTypeGraphNode) *emissionTypeGraphDigester {
	component, components := emissionTypeGraphComponents(nodes)
	cyclic := make([]bool, len(components))
	for index, members := range components {
		if len(members) > 1 {
			cyclic[index] = true
			continue
		}
		member := members[0]
		for _, token := range nodes[member].tokens {
			if token.edge && token.child == member {
				cyclic[index] = true
				break
			}
		}
	}
	return &emissionTypeGraphDigester{
		nodes: nodes, component: component, components: components, cyclic: cyclic,
		digests: make(map[int]string), active: make(map[int]bool),
	}
}

func (d *emissionTypeGraphDigester) digestNode(node int) string {
	if digest, ok := d.digests[node]; ok {
		return digest
	}
	component := d.component[node]
	if d.active[component] {
		panic("cl: recursive type graph escaped its SCC")
	}
	d.active[component] = true
	var fields []string
	if !d.cyclic[component] {
		fields = []string{"acyclic-node-v1"}
		d.appendAcyclicNode(&fields, node)
	} else {
		fields = []string{"cyclic-component-v1"}
		d.appendCyclicComponent(&fields, component, node)
	}
	delete(d.active, component)
	digest := emissionDigest(framedEmissionKey(emissionGoLinknameTypeGraphSchema, framedEmissionKey(fields...)))
	d.digests[node] = digest
	return digest
}

func (d *emissionTypeGraphDigester) appendAcyclicNode(fields *[]string, node int) {
	*fields = append(*fields, "node")
	for _, token := range d.nodes[node].tokens {
		if token.edge {
			*fields = append(*fields, "edge", d.digestNode(token.child))
		} else {
			*fields = append(*fields, "text", token.text)
		}
	}
	*fields = append(*fields, "end-node")
}

func (d *emissionTypeGraphDigester) appendCyclicComponent(fields *[]string, component, root int) {
	local := make(map[int]int, len(d.components[component]))
	var visit func(int)
	visit = func(node int) {
		localID := len(local)
		local[node] = localID
		*fields = append(*fields, "node", strconv.Itoa(localID))
		for _, token := range d.nodes[node].tokens {
			if !token.edge {
				*fields = append(*fields, "text", token.text)
				continue
			}
			if d.component[token.child] != component {
				*fields = append(*fields, "external", d.digestNode(token.child))
				continue
			}
			if target, seen := local[token.child]; seen {
				*fields = append(*fields, "ref", strconv.Itoa(target))
				continue
			}
			*fields = append(*fields, "define")
			visit(token.child)
		}
		*fields = append(*fields, "end-node")
	}
	visit(root)
	if len(local) != len(d.components[component]) {
		panic("cl: recursive type graph traversal omitted an SCC member")
	}
}

// emissionTypeGraphComponents returns Tarjan SCCs for the exact ordered type
// graph. Component numbering is deliberately internal: canonical output uses
// root-local ordered traversal and never serializes these implementation IDs.
func emissionTypeGraphComponents(nodes []emissionTypeGraphNode) ([]int, [][]int) {
	indexes := make([]int, len(nodes))
	lowlinks := make([]int, len(nodes))
	onStack := make([]bool, len(nodes))
	component := make([]int, len(nodes))
	for index := range indexes {
		indexes[index] = -1
		component[index] = -1
	}
	stack := make([]int, 0, len(nodes))
	next := 0
	var components [][]int
	var visit func(int)
	visit = func(node int) {
		indexes[node], lowlinks[node] = next, next
		next++
		stack = append(stack, node)
		onStack[node] = true
		for _, token := range nodes[node].tokens {
			if !token.edge {
				continue
			}
			child := token.child
			if indexes[child] == -1 {
				visit(child)
				if lowlinks[child] < lowlinks[node] {
					lowlinks[node] = lowlinks[child]
				}
			} else if onStack[child] && indexes[child] < lowlinks[node] {
				lowlinks[node] = indexes[child]
			}
		}
		if lowlinks[node] != indexes[node] {
			return
		}
		componentID := len(components)
		var members []int
		for {
			last := len(stack) - 1
			member := stack[last]
			stack = stack[:last]
			onStack[member] = false
			component[member] = componentID
			members = append(members, member)
			if member == node {
				break
			}
		}
		components = append(components, members)
	}
	for node := range nodes {
		if indexes[node] == -1 {
			visit(node)
		}
	}
	return component, components
}
