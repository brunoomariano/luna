package ledger

import "fmt"

// Autonomy is how much runs without a person.
//
// Three named modes rather than a numeric scale. The scale that preceded this had
// eleven values and three meanings, so eight of them were indistinguishable while
// still reading as a choice — the same defect this project already recorded for a
// set of profiles that had silently collapsed into each other.
type Autonomy string

const (
	// Manual sends every gate to a person. It is the default, and the default is
	// deliberately the careful one: an autonomy that has to be asked for is one
	// nobody gets by forgetting.
	Manual Autonomy = "manual"

	// Semi clears the gates that are reversible.
	Semi Autonomy = "semi"

	// Auto clears every gate that declares a floor. It does not clear a block:
	// see Blocks.
	Auto Autonomy = "auto"
)

// rank orders the modes. An unknown mode has no rank and clears nothing.
var autonomyRank = map[Autonomy]int{Manual: 1, Semi: 2, Auto: 3}

// ParseAutonomy reads a mode, naming the alternatives when it cannot.
func ParseAutonomy(value string) (Autonomy, error) {
	mode := Autonomy(value)
	if _, known := autonomyRank[mode]; !known {
		return Manual, fmt.Errorf("autonomy is manual, semi or auto, got %q", value)
	}
	return mode, nil
}

// Valid reports whether this is a mode Luna knows.
func (a Autonomy) Valid() bool {
	_, known := autonomyRank[a]
	return known
}

// Clears reports whether this mode answers a gate whose floor is `floor`.
//
// Both sides are refused when unknown: an unrecognised floor is cleared by
// nothing, and an unrecognised mode clears nothing. Guessing in either direction
// hands a gate to a machine because somebody made a typo.
func (a Autonomy) Clears(floor Autonomy) bool {
	have, ok := autonomyRank[a]
	if !ok {
		return false
	}
	need, ok := autonomyRank[floor]
	if !ok {
		return false
	}
	return have >= need
}

// Blocks reports whether a phase may stop for missing information at this mode.
//
// It is always true, and it is a function rather than a comment so that the rule
// has somewhere to be tested. INV-5: the difference between running without
// asking and running without thinking is the whole value of an unattended fleet,
// and a fleet that cannot stop produces expensive noise.
func (Autonomy) Blocks() bool { return true }
