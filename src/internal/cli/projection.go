package cli

import (
	"context"
	"fmt"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/registry"
)

// projectWith mirrors where a task is into the registry.
//
// It is a projection and never a source: the log is the state, and nothing read
// from here is ever used to decide anything (ADR-0065). What it buys is the one
// question a per-repository log cannot answer — what is happening across every
// checkout (ADR-0054).
//
// Nil when there is no registry, which is a project that never adopted one. Then
// nothing is projected and nothing else changes.
func projectWith(reg Registry) func(context.Context, fsm.TaskState) error {
	if reg == nil {
		return nil
	}

	return func(ctx context.Context, state fsm.TaskState) error {
		// Read first, because Move is guarded and the guard needs to know what the
		// registry currently says — not what Luna thinks it should say.
		task, err := reg.Task(ctx, state.ID)
		if err != nil {
			return fmt.Errorf("reading %s before projecting it: %w", state.ID, err)
		}

		if want := registryStatus(state.Status); want != task.Status {
			if err := reg.Move(ctx, state.ID, task.Status, want); err != nil {
				return err
			}
		}

		// The stage rides as a label, because beads has no concept of one and
		// should not grow one — the flow is Luna's (INV-core-1).
		if state.Stage != "" {
			return reg.EnterStage(ctx, state.ID, string(state.Stage))
		}
		return nil
	}
}

// registryStatus maps Luna's status onto the four beads uses.
//
// The two vocabularies are kept apart on purpose (ADR-0054), so this is the one
// place they meet. It is lossy in one direction and that is correct: beads
// answers "is anyone stuck", not "which of six states is this".
//
// `awaiting_gate` maps to blocked, which is the only mapping worth arguing
// about. It is not an anomaly — a gate is a planned pause — but from another
// checkout the useful question is "does this need me", and a task waiting on a
// person answers yes exactly as a blocked one does. INV-core-12 is that half:
// what needs a human is locatable without anyone remembering.
func registryStatus(status fsm.Status) registry.Status {
	switch status {
	case fsm.StatusDone, fsm.StatusAbandoned:
		return registry.StatusClosed
	case fsm.StatusBlocked, fsm.StatusAwaitingGate:
		return registry.StatusBlocked
	case fsm.StatusReady:
		return registry.StatusOpen
	default:
		// running and stage_done are both "Luna has this".
		return registry.StatusInProgress
	}
}
