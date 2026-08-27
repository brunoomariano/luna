package fsm

// Condition decides whether a stage enters the flow, and carries a name so the
// decision can be identified rather than only executed.
//
// The name is the point. A bare `func(TaskContext) bool` is history — it decides
// which stages a task should have walked through — and a function has no identity
// a fingerprint can record, so changing `diagnose` from "is a bug" to "is a
// feature" would rewrite what a past log means with nothing noticing.
//
// Naming it does not make the fingerprint able to see inside the function. What it
// buys is a stable handle a person chose deliberately: renaming the condition is
// how you say "this rule is not the rule it was", and the discipline that makes
// that reliable is the same one the log's action names already rely on — they are
// hand-written string constants for exactly this reason (see the note in the
// codec that the log outlives the code).
type Condition struct {
	// Name identifies the rule. Two conditions with the same name are the same
	// rule as far as a replay is concerned.
	Name string

	// Applies is the rule itself.
	Applies func(TaskContext) bool
}

// Always is the zero condition: a stage with no `When` enters unconditionally.
//
// It has an empty name rather than "always", so a flow that never used conditions
// fingerprints the same as one that spells the default out.
var Always = Condition{}

// Met reports whether the condition holds. The zero value always holds.
func (c Condition) Met(ctx TaskContext) bool {
	if c.Applies == nil {
		return true
	}
	return c.Applies(ctx)
}

// The conditions the shipped flow uses.
//
// Declared here rather than inline in DefaultFlow so that two stages sharing a
// rule share its name too, and a fingerprint says it once. The shipped flow uses
// two of them since the merges; the rest stay registered because a project flow
// can name any of them, and a condition nothing ships is still one somebody can.
var (
	// IsBug gates the stage that finds out why something broke.
	IsBug = Condition{
		Name:    "is-bug",
		Applies: func(c TaskContext) bool { return c.Kind == KindBug },
	}

	// IsFeatureOrBug governs the stages whose cost only pays off when there is new
	// behaviour or a defect to fix.
	IsFeatureOrBug = Condition{
		Name:    "is-feature-or-bug",
		Applies: func(c TaskContext) bool { return c.Kind == KindFeature || c.Kind == KindBug },
	}

	// NotChore keeps QA off a one-line chore.
	NotChore = Condition{
		Name:    "not-chore",
		Applies: func(c TaskContext) bool { return c.Kind != KindChore },
	}

	// NotDocs keeps code review off a change with no code in it.
	NotDocs = Condition{
		Name:    "not-docs",
		Applies: func(c TaskContext) bool { return c.Kind != KindDocs },
	}

	// TouchedStructure is a condition about a fact discovered mid-run rather than
	// about the nature of the task: you only know the change touched the structure
	// after looking at what build produced.
	TouchedStructure = Condition{
		Name:    "touched-structure",
		Applies: func(c TaskContext) bool { return c.HasFact(TouchesStructure) },
	}

	// TriagedAsBug gates the investigation on what the intake concluded, rather
	// than on what the person opening the task declared.
	//
	// A second condition rather than a widening of IsBug, and the two are not
	// interchangeable. `fix` is chosen by somebody who already knows what broke and
	// has no intake to conclude anything, so its investigation has to key on the
	// kind. `full` reads the task and the code first, and a trail that asks a
	// person to classify before either has been read is asking too early.
	//
	// Widening `is-bug` to mean "either" was the alternative and is worse: one name
	// for two sources is a condition whose reader cannot tell which answered.
	TriagedAsBug = Condition{
		Name:    "triaged-as-bug",
		Applies: func(c TaskContext) bool { return c.HasFact(TriagedBug) },
	}
)
