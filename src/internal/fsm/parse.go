package fsm

import (
	"fmt"
	"strings"
)

// ParseScope turns a configured name into a scope, refusing what it does not
// know.
//
// The refusal matters more than it looks. A scope is what separates a green
// suite from a file that exists, so a typo that fell through to some
// default would be the laundering the scopes were introduced to prevent — and it
// would do it silently, in a file nobody re-reads.
func ParseScope(name string) (Scope, error) {
	return parseEnum(name, KnownScopes(), "scope")
}

// KnownGateKinds is every gate kind this build understands.
//
// A closed set for the same reason the others are: a stage file naming a gate
// kind that does not exist has asked for a pause nobody will ever be shown, and
// falling back to "no gate" would make an unattended run out of a supervised one.
func KnownGateKinds() []GateKind {
	return []GateKind{GateConfirm, GateReviewArtifact, GateLoopCeiling}
}

// ParseGateKind turns a configured name into a gate kind.
func ParseGateKind(name string) (GateKind, error) {
	return parseEnum(name, KnownGateKinds(), "gate kind")
}

// ShippedConditions are the conditions a stage file may name.
//
// Deliberately a closed set rather than an expression language. A condition
// decides which stages a task should have walked through, which makes it history
// — and an arbitrary predicate in a file is a flow whose past cannot
// be reconstructed, because the predicate that produced it may no longer exist.
//
// A flow that needs a condition this build has never heard of needs a build that
// has, which is the same trade the harness table makes.
func ShippedConditions() []Condition {
	return []Condition{IsBug, IsFeatureOrBug, NotChore, NotDocs, TouchedStructure}
}

// ParseCondition resolves a condition by name.
//
// An empty name is the unconditional stage, which is the common case and is
// written by leaving `when` out rather than by naming something.
func ParseCondition(name string) (Condition, error) {
	if name == "" {
		return Always, nil
	}
	for _, condition := range ShippedConditions() {
		if condition.Name == name {
			return condition, nil
		}
	}

	names := make([]string, 0, len(ShippedConditions()))
	for _, condition := range ShippedConditions() {
		names = append(names, condition.Name)
	}
	return Condition{}, fmt.Errorf("unknown condition %q (%s)", name, strings.Join(names, ", "))
}

// parseEnum resolves a name against a closed set, naming what was expected.
func parseEnum[T ~string](name string, known []T, what string) (T, error) {
	for _, value := range known {
		if T(name) == value {
			return value, nil
		}
	}

	names := make([]string, 0, len(known))
	for _, value := range known {
		names = append(names, string(value))
	}

	var zero T
	return zero, fmt.Errorf("unknown %s %q (%s)", what, name, strings.Join(names, ", "))
}
