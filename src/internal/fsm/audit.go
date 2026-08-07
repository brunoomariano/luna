package fsm

// ContractGap is a stage requiring artifacts that no earlier stage produces.
// It carries both the stage and what is missing: a message that only says
// "broken contract" costs a debugging session.
type ContractGap struct {
	Stage   StageID
	Missing []Artifact
}

// AuditContract walks the flow in order and reports every stage whose Requires
// is not satisfied by some earlier stage.
//
// This is the first of the contract's three checks (INV-core-3), and the only one
// that runs without executing anything: it detects a flow broken *on paper*,
// before any agent is called. The other two — on entry and on exit — can only
// fail once the task is already running.
//
// ProducesForHuman deliberately stays out of the available set: an audit report
// is read by a person, not consumed by the flow, and satisfying a Requires with
// one would make the field decorative (ADR-0021).
//
// Order is what gets checked, not existence: an artifact produced after the stage
// that requires it does not satisfy that stage.
func AuditContract(flow []Stage) []ContractGap {
	available := map[Artifact]bool{TaskID: true}
	var gaps []ContractGap

	for _, stage := range flow {
		if missing := missingFrom(stage.Requires, available); len(missing) > 0 {
			gaps = append(gaps, ContractGap{Stage: stage.ID, Missing: missing})
		}
		for _, produced := range stage.Produces {
			available[produced] = true
		}
	}

	return gaps
}

// missingFrom returns the required artifacts that are not available yet, keeping
// declaration order — whoever reads the report compares it against the contract
// they wrote.
//
// Shared by both contract checks, and that is the point: the static one asks it
// against the artifacts an earlier stage would have produced, the entry one
// against the artifacts this task actually holds. Same question, different
// moment — so a change to what counts as "available" cannot drift between them.
func missingFrom(required []Artifact, available map[Artifact]bool) []Artifact {
	var missing []Artifact
	for _, r := range required {
		if !available[r] {
			missing = append(missing, r)
		}
	}
	return missing
}
