package fsm

// BlockKind says what kind of thing stopped a task, as a closed set a reader can
// group by.
//
// The prose in `Blocked` says what happened to this task; this says which of the
// handful of shapes it is. A fleet report needs the second — "three tasks need a
// person and one needs a bigger machine" is a morning's work sorted, while four
// paragraphs of prose is four paragraphs to read.
//
// It is recorded beside the prose rather than parsed back out of it: a message
// somebody rewords would silently reclassify every task that hit it.
type BlockKind string

const (
	// BlockContract is a stage whose inputs were not there, or whose outputs did
	// not arrive after being asked for. The flow is sound and the work is not.
	BlockContract BlockKind = "contract"

	// BlockCheck is a verification that ran and did not pass, or passed at a
	// weaker scope than the contract declared. Something objective said no.
	BlockCheck BlockKind = "failed-check"

	// BlockBudget is the spending ceiling. Nothing is wrong with the work.
	BlockBudget BlockKind = "over-budget"

	// BlockTooling is the machinery: a missing binary, a git that will not run.
	// It says nothing about the task and is usually true of every task at once,
	// which is exactly why it is worth telling apart from the rest.
	BlockTooling BlockKind = "tooling"

	// BlockNode is an agent that failed its attempts. The work was tried and did
	// not get there.
	BlockNode BlockKind = "failed-node"

	// BlockNoProgress is a loop that stopped converging with nobody waiting to
	// decide about it.
	BlockNoProgress BlockKind = "no-progress"
)

// Product is what a task's own evidence says about the work it delivered.
//
// Deliberately not a score. Luna cannot judge whether code is good; it can report
// what its own checks proved, and the whole point of keeping this apart from
// Operation is that a task can deliver correct code and still stop badly.
type Product string

const (
	// ProductVerified is every artifact the flow asked for, proven at the scope
	// its contract declared. It is available only to a task that finished,
	// because that is the only state the reducer reaches by closing every stage.
	ProductVerified Product = "verified"

	// ProductPartial is some artifacts proven and the flow not finished.
	ProductPartial Product = "partial"

	// ProductFailed is a check that ran and said no. It outranks the others: one
	// failing verdict is the answer, whatever else passed.
	ProductFailed Product = "failed"

	// ProductNone is a task that has proven nothing yet.
	ProductNone Product = "none"

	// ProductSimulated is a task whose stages ran no agent. It is its own value
	// rather than a flag beside one of the others, because a simulation reported
	// as `verified` with a note next to it is the lie `--dry-run` already told
	// once: a reader sees the word, not the note.
	ProductSimulated Product = "simulated"
)

// Operation is what the flow did, as opposed to what the work is.
type Operation string

const (
	// OperationClean is a task that finished. Nothing is owed and nobody is
	// waiting.
	OperationClean Operation = "clean"

	// OperationWaiting is a task suspended at a gate. It is not a failure, and it
	// is not progress either.
	OperationWaiting Operation = "waiting"

	// OperationStopped is a task that blocked. What kind is BlockKind's answer.
	OperationStopped Operation = "stopped"

	// OperationRunning is a task still going.
	OperationRunning Operation = "running"

	// OperationCalledOff is a task a person abandoned.
	OperationCalledOff Operation = "called-off"
)

// Product reports what this task's evidence says about the work.
func (s TaskState) Product() Product {
	if s.Simulated {
		return ProductSimulated
	}

	proven := 0
	for _, evidence := range s.Evidence {
		if evidence.Verdict == VerdictFailed {
			return ProductFailed
		}
		if evidence.Passing() {
			proven++
		}
	}

	switch {
	case s.Status == StatusDone:
		return ProductVerified
	case proven > 0:
		return ProductPartial
	default:
		return ProductNone
	}
}

// Operation reports what the flow did with this task.
func (s TaskState) Operation() Operation {
	switch s.Status {
	case StatusDone:
		return OperationClean
	case StatusAbandoned:
		return OperationCalledOff
	case StatusAwaitingGate:
		return OperationWaiting
	case StatusBlocked:
		return OperationStopped
	default:
		return OperationRunning
	}
}
