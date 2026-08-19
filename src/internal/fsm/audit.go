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
// This is the first of the contract's three checks (INV-3), and the only one
// that runs without executing anything: it detects a flow broken *on paper*,
// before any agent is called. The other two — on entry and on exit — can only
// fail once the task is already running.
//
// ProducesForHuman deliberately stays out of the available set: an audit report
// is read by a person, not consumed by the flow, and satisfying a Requires with
// one would make the field decorative.
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
// runs without an agent, which is right for `setup` and `commit` and
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

// NameGap is a stage whose id is too long for the names built around it.
type NameGap struct {
	Stage StageID

	// Budget is how many characters a task id could have alongside this stage id
	// before the name they share stops fitting. Zero or less means the stage name
	// alone does not fit.
	Budget int
}

// AuditFlowNames reports stages whose ids leave no room for a task id.
//
// A stage id travels into the same names a task id does — it is written into the
// order and the log beside it, and a flow is read by whoever has to find those.
// The ceiling is the one MaxTaskIDLen documents: a path component, 255 bytes,
// shared between the fixed parts and whatever varies.
//
// It is a static check for the same reason AuditContract is one: a custom flow
// can bring a stage id nobody sized, and the alternative to saying so here is
// finding out from a name that was silently cut.
func AuditFlowNames(flow []Stage) []NameGap {
	var gaps []NameGap

	for _, stage := range flow {
		budget := nameComponentLimit - len(stage.ID)
		if budget < MaxTaskIDLen {
			gaps = append(gaps, NameGap{Stage: stage.ID, Budget: budget})
		}
	}
	return gaps
}

// nameComponentLimit is the room a stage id and a task id share. It is the same
// budget MaxTaskIDLen is cut from — one path component, with a margin for the
// repository and role names that vary per project.
const nameComponentLimit = 128

// ContextGap is a stage asking to continue a session it must not continue.
type ContextGap struct {
	Stage StageID

	// From is the stage whose session this one would have inherited, and Role is
	// what that stage ran as. Both are in the report because the reason is the
	// pair: continuing is fine, continuing across a change of role is not.
	From StageID
	Role string
}

// AuditContextChain reports every stage declaring `context = "live"` that would
// inherit a session from a different role.
//
// When fresh context stopped being a rule, one part of it did not: a reviewer
// must not continue the implementer's session. It would be reading its own
// reasoning instead of the delivery, and that is the one thing verification at
// the exit cannot stand in for — a review that confirms is not a review.
//
// Static for the same reason the contract check is: the alternative is finding
// out from a review that agreed with everything, which reads exactly like a
// stage that went well.
func AuditContextChain(flow []Stage) []ContextGap {
	if len(flow) == 0 {
		return nil
	}

	var gaps []ContextGap

	// The first stage is judged alone: there is nothing before it to continue.
	if first := flow[0]; !first.Context.Fresh() {
		// Named as a gap against itself, so the report says what is wrong rather
		// than pointing at a stage that is absent.
		gaps = append(gaps, ContextGap{Stage: first.ID, Role: first.Role})
	}

	// The rest walk as pairs rather than by index, which is what the rule
	// actually is — a stage and the one whose session it would inherit.
	for i, stage := range flow[1:] {
		previous := flow[i]
		if stage.Context.Fresh() || previous.Role == stage.Role {
			continue
		}
		gaps = append(gaps, ContextGap{Stage: stage.ID, From: previous.ID, Role: previous.Role})
	}
	return gaps
}
