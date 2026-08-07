package fsm

import (
	"errors"
	"fmt"
)

// ErrUnknownStage is returned when the current stage is not part of the flow.
//
// It exists so the caller can tell a typo from the end of the flow. Both would
// otherwise look like "no next stage", and the first is a bug while the second is
// the happy path.
var ErrUnknownStage = errors.New("stage is not part of the flow")

// NextStage answers which stage comes after current, skipping the ones whose
// condition does not hold in this context.
//
// The FSM decides this — never the model (INV-core-1). The three return values
// separate three different outcomes that callers must not conflate:
//
//   - (stage, true, nil)    — the next stage to run;
//   - ("", false, nil)      — the flow is over, which is the happy path;
//   - ("", false, err)      — current is not in the flow, which is a bug.
//
// Pass the zero StageID as current to get the flow's entry stage.
//
// A skipped stage is not an error and not a gap: on a chore, qa simply does not
// exist in that flow (ADR-0014). Conditions are evaluated against the context as
// it is now, so a stage gated on a fact discovered mid-run — architecture on
// TouchesStructure — enters as soon as the fact appears.
func NextStage(flow []Stage, current StageID, ctx TaskContext) (StageID, bool, error) {
	start := 0

	if current != "" {
		i := indexOf(flow, current)
		if i < 0 {
			return "", false, fmt.Errorf("%w: %q", ErrUnknownStage, current)
		}
		start = i + 1
	}

	for _, stage := range flow[start:] {
		if stage.AppliesTo(ctx) {
			return stage.ID, true, nil
		}
	}

	return "", false, nil
}

// MissingFor lists the artifacts a stage requires that are not in the context.
//
// This is the contract's entry check (INV-core-3) — the sibling of AuditContract.
// They ask different questions: the static one asks whether the flow holds
// together on paper, this one asks whether this task, right now, can start this
// stage. A flow can pass the static check and still be short an input at runtime,
// because a conditional stage was skipped or an artifact was invalidated
// (ADR-0020).
//
// Declaration order is preserved: whoever reads the answer compares it against
// the contract they wrote.
func MissingFor(stage Stage, ctx TaskContext) []Artifact {
	return missingFrom(stage.Requires, ctx.Artifacts)
}

// indexOf returns the position of the stage in the flow, or -1.
//
// Kept private and explicit rather than inlined: a caller that treats the -1 as a
// starting point silently restarts the task, which is exactly the failure
// NextStage turns into ErrUnknownStage.
func indexOf(flow []Stage, id StageID) int {
	for i, stage := range flow {
		if stage.ID == id {
			return i
		}
	}
	return -1
}
