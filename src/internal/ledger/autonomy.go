package ledger

import "fmt"

// Autonomy is how much runs without a person, as recorded.
//
// Three named modes rather than a numeric scale. The scale that preceded this had
// eleven values and three meanings, so eight of them were indistinguishable while
// still reading as a choice — the same defect this project already recorded for a
// set of profiles that had silently collapsed into each other.
//
// What Luna does with a mode is *record it*. Which mode clears which gate is a
// flow decision, and it belongs to whoever conducts. This type once carried
// `Clears` and `Blocks` to answer that here; both were tested, called by nothing,
// and removed — putting the answer in Luna is the thing the September 2026
// redesign undid.
type Autonomy string

const (
	// Manual is the default, and the default is deliberately the careful one: an
	// autonomy that has to be asked for is one nobody gets by forgetting. The
	// conductor sends every gate to a person under it.
	Manual Autonomy = "manual"

	// Semi is the middle mode, under which a conductor answers the reversible
	// gates itself.
	Semi Autonomy = "semi"

	// Auto is the unattended mode. Every mode still blocks on missing
	// information, including this one — see INV-5. That rule is the conductor's
	// to honour; Luna records the mode and resolves nothing against it.
	Auto Autonomy = "auto"
)

// ParseAutonomy reads a mode, naming the alternatives when it cannot.
//
// The closed set is the whole point: a typo recorded as an autonomy is a mode
// nobody can act on, and it would sit in an append-only record forever.
func ParseAutonomy(value string) (Autonomy, error) {
	switch Autonomy(value) {
	case Manual, Semi, Auto:
		return Autonomy(value), nil
	}
	return Manual, fmt.Errorf("autonomy is manual, semi or auto, got %q", value)
}
