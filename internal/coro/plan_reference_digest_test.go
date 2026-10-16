//go:build !llgo

package coro

import (
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestCoroPlanDigestBindsFrozenPublicationReferences(t *testing.T) {
	plan, _ := buildPlanDigestTestPlan(t, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
	metadata := validPlanDigestMetadata()
	original := append([]ReferenceEdge(nil), plan.plan.references...)
	if len(original) == 0 {
		t.Fatal("fixture has no publication references")
	}
	before, err := plan.CoroPlanDigest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*ReferenceEdge){
		"physical":    func(r *ReferenceEdge) { r.PhysicalABI = !r.PhysicalABI },
		"raw":         func(r *ReferenceEdge) { r.RawPlain = !r.RawPlain },
		"sync":        func(r *ReferenceEdge) { r.SyncOnly = !r.SyncOnly },
		"observation": func(r *ReferenceEdge) { r.ObservationOnly = !r.ObservationOnly },
	} {
		t.Run(name, func(t *testing.T) {
			plan.plan.references = append([]ReferenceEdge(nil), original...)
			change(&plan.plan.references[0])
			after, err := plan.CoroPlanDigest(metadata)
			if err == nil && after == before {
				t.Fatal("publication policy is absent from digest")
			}
		})
	}
	for _, bad := range [][]ReferenceEdge{
		{{Owner: "missing", Target: original[0].Target}},
		{original[0], original[0]},
		{{Owner: original[0].Owner, Target: original[0].Target, RawPlain: true, SyncOnly: true}},
		{{Owner: original[0].Owner, Target: original[0].Target, RawPlain: true, ObservationOnly: true}},
	} {
		plan.plan.references = bad
		if _, err := plan.CoroPlanDigest(metadata); err == nil {
			t.Fatal("malformed publication reference was accepted")
		}
	}
	plan.plan.references = original
}
