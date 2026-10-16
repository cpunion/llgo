package coro

import "fmt"

// ProgramPredicateRequirements binds a compiled entry to exactly the optional
// program predicates consumed by its effect/ABI specialization. A false value
// is still a requirement: omitting that distinction would let a consumer reuse
// a plain entry after enabling a service that makes its original body suspend.
// Service demand (ProgramCapabilities) and specialization are independent.
type ProgramPredicateRequirements struct {
	Mask   ProgramCapabilities `json:"mask"`
	Values ProgramCapabilities `json:"values"`
}

func (requirements ProgramPredicateRequirements) Valid() bool {
	return requirements.Mask&^ProgramPredicateMask() == 0 && requirements.Values&^requirements.Mask == 0
}

func (requirements ProgramPredicateRequirements) Compatible(actual ProgramCapabilities) bool {
	return requirements.Valid() && actual.Valid() && actual&requirements.Mask == requirements.Values
}

// Join propagates a producer's exact requirements through the already-frozen
// call plan. Contradictory library specializations cannot form one program.
func (requirements ProgramPredicateRequirements) Join(other ProgramPredicateRequirements) (ProgramPredicateRequirements, error) {
	if !requirements.Valid() || !other.Valid() {
		return ProgramPredicateRequirements{}, fmt.Errorf("coro: invalid program predicate requirements")
	}
	if (requirements.Values^other.Values)&requirements.Mask&other.Mask != 0 {
		return ProgramPredicateRequirements{}, fmt.Errorf("coro: incompatible program predicate specializations")
	}
	return ProgramPredicateRequirements{Mask: requirements.Mask | other.Mask, Values: requirements.Values | other.Values}, nil
}
