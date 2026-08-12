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

// RoleGap is a stage that produces something only judgement can produce and names
// no role to produce it.
type RoleGap struct {
	Stage    StageID
	Produces []Artifact
}

// AuditRoles reports every stage that needs an agent and has none.
//
// It exists because the mechanical path is silent by nature. A stage with no role
// runs without an agent (ADR-0040), which is right for `setup` and `commit` and
// catastrophic for one that was supposed to write a contract: it would run,
// deliver nothing, and look like it worked.
//
// Like AuditContract, this catches a flow broken on paper — before any agent is
// called, and before a task spends an afternoon producing nothing.
func AuditRoles(flow []Stage) []RoleGap {
	var gaps []RoleGap

	for _, stage := range flow {
		if !stage.NeedsRole() {
			continue
		}

		var needing []Artifact
		for _, artifact := range append(append([]Artifact{}, stage.Produces...), stage.ProducesForHuman...) {
			if judgement[artifact] {
				needing = append(needing, artifact)
			}
		}
		gaps = append(gaps, RoleGap{Stage: stage.ID, Produces: needing})
	}

	return gaps
}

// NameGap is a stage whose id leaves no room for a task id inside an agent name.
type NameGap struct {
	Stage StageID

	// Budget is how many characters a task id could have if this stage were the
	// longest. Zero or less means the stage name alone does not fit.
	Budget int
}

// AuditFlowNames reports stages whose ids squeeze the task id past its limit.
//
// herdr caps an agent name at 32 characters (ADR-0036) and Luna builds that name
// as `luna-<id>-<stage>`, so a long stage name and a long task id cannot both fit.
// MaxTaskIDLen is derived from the longest stage in the *shipped* flow, and a
// custom flow (ADR-0017) can break that arithmetic.
//
// It is a static check for the same reason AuditContract is one: the alternative
// is discovering it when two stages of one task produce the same truncated agent
// name and prompt each other's pane.
func AuditFlowNames(flow []Stage) []NameGap {
	var gaps []NameGap

	for _, stage := range flow {
		// "luna-" + id + "-" + stage, within 32.
		budget := agentNameLimit - len("luna-") - len("-") - len(stage.ID)
		if budget < MaxTaskIDLen {
			gaps = append(gaps, NameGap{Stage: stage.ID, Budget: budget})
		}
	}
	return gaps
}
