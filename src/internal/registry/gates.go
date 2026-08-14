package registry

import (
	"encoding/json"
	"fmt"
)

// GateChecks are the commands a person declared as the answer to one gate.
//
// The shape is measured against bd 1.2.1 rather than read from its docs: a task
// carries `metadata.luna_gates`, keyed by gate kind, and each entry holds the
// commands that answer that gate.
//
//	bd create "..." --metadata '{"luna_gates": {
//	   "approve-plan": {"checks": ["make fmt", "make typecheck"]},
//	   "approve-spec": {"checks": ["make ci"]}
//	}}'
//
// Keyed by gate rather than global because a task may want the whole suite at
// one gate and something narrower at another. `--metadata` merges by key on
// update, so writing Luna's entry leaves another tool's untouched — also
// measured, not assumed.
type GateChecks struct {
	Checks []string `json:"checks,omitempty"`
}

// lunaGates is the metadata key Luna owns. Namespaced because the metadata
// object is shared with whatever else writes to the same task.
const lunaGates = "luna_gates"

// ChecksFor returns the commands declared for one gate kind, and whether the
// task declared anything for it at all.
//
// The two are separate answers because they mean different things downstream:
// "no checks declared" sends the gate to the judgement half, while "declared and
// empty" is a person saying this gate has no mechanical answer. Collapsing them
// into a nil slice would make the second unsayable.
//
// A gate this task says nothing about is the normal case — it is every task
// today — so it is a state rather than an error.
//
// A `luna_gates` that will not decode is an error, and a loud one. It is the
// person's own declaration about their own task, so a malformed one is a mistake
// they want told about — silently treating it as "no checks" would answer the
// gate by asking them, which is exactly what they were trying to stop doing.
func (t Task) ChecksFor(gate string) (checks []string, declared bool, err error) {
	raw, ok := t.Metadata[lunaGates]
	if !ok {
		return nil, false, nil
	}

	var gates map[string]GateChecks
	if err := json.Unmarshal(raw, &gates); err != nil {
		return nil, false, fmt.Errorf(
			"reading %s.%s on %s: %w", lunaGates, gate, t.ID, err,
		)
	}

	entry, ok := gates[gate]
	if !ok {
		return nil, false, nil
	}
	return entry.Checks, true, nil
}
