package lead

import (
	"context"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// BudgetJudge retries while the task's budget allows and escalates when it does
// not.
//
// It is the judgement layer with no judgement in it, and shipping it is the point:
// without a Judge the lead blocked on the first failure, so the retry budget
// ADR-0011 describes was never spent and the "hybrid" lead of ADR-0002 was, in
// production, a purely deterministic one. A rule that reads in three lines is a
// better default than a behaviour nobody chose.
//
// It reads the budget from the state rather than holding one. The reducer already
// counts attempts and blocks when they run out, so a second copy here would be a
// number that disagrees with the log after the first restart — and the log is the
// state (INV-core-2).
//
// A model-backed judge is the interesting version and remains why the interface
// exists: ADR-0002's argument comes from a case where the machine was wrong and a
// model caught it. This is the floor beneath that, not a replacement.
type BudgetJudge struct{}

// OnFailure retries while attempts remain, and blocks once they do not.
//
// Blocking here rather than letting the budget run out through Fail keeps one
// decision to one event. Recording failures until the reducer escalates would put
// three attempts in the log where there was one choice to stop, and the history is
// the audit (ADR-0011).
func (BudgetJudge) OnFailure(_ context.Context, state fsm.TaskState, _ string) Decision {
	if state.Retry.Attempts < state.Retry.Max {
		return DecideRetry
	}
	return DecideBlock
}
