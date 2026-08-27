package fsm

import (
	"strings"
	"testing"
	"testing/fstest"
)

// TestEveryShippedFlowPassesEveryStaticCheck is the guard that grows with the
// stock.
//
// The single-flow version of this asked about `DefaultFlow` and nothing else, so
// a second flow could ship with a requirement nothing produces and the suite
// would stay green while a task walking it stalled two stages past the cause.
// This one enumerates, so a flow added tomorrow is covered by a test written
// today — which is the only kind of coverage that survives a stock that grows.
func TestEveryShippedFlowPassesEveryStaticCheck(t *testing.T) {
	names := FlowNames()
	if len(names) < 2 {
		t.Fatalf("want several shipped flows, got %v", names)
	}

	for _, name := range names {
		flow, err := FlowNamed(name)
		if err != nil {
			t.Fatalf("flow %q does not load: %v", name, err)
		}
		if len(flow) == 0 {
			t.Errorf("flow %q has no stages", name)
		}

		for _, gap := range AuditContract(flow) {
			t.Errorf("%s: stage %q requires %v, which no earlier stage produces",
				name, gap.Stage, gap.Missing)
		}
		for _, gap := range AuditAgents(flow) {
			t.Errorf("%s: stage %q produces %v and names no agent",
				name, gap.Stage, gap.Produces)
		}
		for _, gap := range AuditFlowNames(flow) {
			t.Errorf("%s: stage %q leaves only %d characters for a task id",
				name, gap.Stage, gap.Budget)
		}
		for _, gap := range AuditContextChain(flow) {
			t.Errorf("%s: stage %q asks to continue %q, which is briefed differently",
				name, gap.Stage, gap.From)
		}
		for _, gap := range AuditGateCriteria(flow) {
			t.Errorf("%s: stage %q declares %q readable and judges no such criterion",
				name, gap.Stage, gap.Criterion)
		}
	}
}

// TestEveryShippedFlowHasItsOwnFingerprint. Two flows that fingerprint alike are
// one flow with two names, and a task opened under either would replay against
// whichever the name resolved to — which is the silent substitution the
// fingerprint exists to refuse.
func TestEveryShippedFlowHasItsOwnFingerprint(t *testing.T) {
	seen := map[FlowFingerprint]string{}

	for _, name := range FlowNames() {
		flow, err := FlowNamed(name)
		if err != nil {
			t.Fatalf("flow %q does not load: %v", name, err)
		}
		print := Fingerprint(flow)
		if other, clash := seen[print]; clash {
			t.Errorf("flows %q and %q fingerprint alike (%s)", name, other, print)
		}
		seen[print] = name
	}
}

// TestTheDefaultFlowIsTheHeaviest. The default is what a task gets when nobody
// chose, and the direction of that mistake has to be towards more supervision —
// the same asymmetry ParseKnob draws. A default that stripped stages would let an
// unset field quietly remove the planning and the review from work that wanted
// them.
func TestTheDefaultFlowIsTheHeaviest(t *testing.T) {
	longest := len(DefaultFlow())

	for _, name := range FlowNames() {
		flow, err := FlowNamed(name)
		if err != nil {
			t.Fatalf("flow %q does not load: %v", name, err)
		}
		if len(flow) > longest {
			t.Errorf("flow %q has %d stages and the default %q has %d — "+
				"a task that named no flow would get less supervision than one that did",
				name, len(flow), DefaultFlowName, longest)
		}
	}
}

// TestAnUnknownFlowIsRefusedByName. The alternative is a task silently opened
// against the default when somebody meant something else, and by the time that
// shows up the opening event is in an append-only log.
func TestAnUnknownFlowIsRefusedByName(t *testing.T) {
	_, err := FlowNamed("nightly-lean")
	if err == nil {
		t.Fatal("a flow this build does not have resolved to something")
	}
	if got := err.Error(); !strings.Contains(got, "nightly-lean") || !strings.Contains(got, DefaultFlowName) {
		t.Errorf("the refusal names neither what was asked nor what exists: %s", got)
	}
}

// TestTheLeanFlowsSkipTheStagesTheyExistToSkip states what each flow is *for*, so
// that a well-meaning edit adding `plan` back to `fix` fails a test instead of
// quietly making the lean flow the heavy one.
func TestTheLeanFlowsSkipTheStagesTheyExistToSkip(t *testing.T) {
	for name, absent := range map[string][]StageID{
		"fix":   {"intake", "plan", "refactor", "audit"},
		"chore": {"intake", "diagnose", "plan", "refactor", "audit"},
	} {
		flow, err := FlowNamed(name)
		if err != nil {
			t.Fatalf("flow %q does not load: %v", name, err)
		}
		for _, id := range absent {
			// Not stageIn: it returns Stage{ID: id} for a stage a flow does not
			// have, so comparing the id back would report every absent stage as
			// present. It echoes the id so the reducer can name it in an error.
			if _, carried := findStage(flow, id); carried {
				t.Errorf("flow %q carries %q, which is one of the stages it exists to skip", name, id)
			}
		}
	}
}

// TestTheLeanFlowsProveTheGreenWithACommandAndNoAgent is the cost claim those
// flows are built on, as a test rather than a comment: `ci_green` is a command's
// verdict, so the stage that owes only it needs no agent, and a stage naming
// none starts none.
func TestTheLeanFlowsProveTheGreenWithACommandAndNoAgent(t *testing.T) {
	for _, name := range []string{"fix", "chore"} {
		flow, err := FlowNamed(name)
		if err != nil {
			t.Fatalf("flow %q does not load: %v", name, err)
		}

		verify, carried := findStage(flow, "verify")
		if !carried {
			t.Fatalf("flow %q has no verify stage", name)
		}
		if !verify.Mechanical() {
			t.Errorf("flow %q pays a model for verify (agent %q)", name, verify.Agent)
		}
		command, declared := verify.Verifiers["ci_green"].(Command)
		if !declared {
			t.Fatalf("flow %q proves ci_green with %T, not a command", name, verify.Verifiers["ci_green"])
		}
		if command.Proves() != ScopeFull {
			t.Errorf("flow %q claims %q for the whole pipeline", name, command.Proves())
		}
	}
}

// findStage says whether a flow carries a stage, which stageIn deliberately does
// not: it answers with Stage{ID: id} so an error can name the stage that was
// asked for, and that makes it useless for asking whether the stage is there.
func findStage(flow []Stage, id StageID) (Stage, bool) {
	for _, stage := range flow {
		if stage.ID == id {
			return stage, true
		}
	}
	return Stage{}, false
}

// TestLoadFlowsRefusesADirectoryWithNoFlows. A build whose stock loaded as
// nothing would pass every static check there is — there would be no stage to
// find a gap in — and then open no task at all.
func TestLoadFlowsRefusesADirectoryWithNoFlows(t *testing.T) {
	_, err := LoadFlows(fstest.MapFS{"flows/notes.txt": &fstest.MapFile{}}, "flows")
	if err == nil {
		t.Fatal("a directory holding no flow directories loaded")
	}
	if !strings.Contains(err.Error(), "flows") {
		t.Errorf("the refusal does not name the directory: %v", err)
	}
}

// TestLoadFlowsNamesTheFlowThatDoesNotParse. One broken stage file in one flow
// stops the build, and the message has to say which flow — otherwise the reader
// is left grepping every directory for a stage that will not parse.
func TestLoadFlowsNamesTheFlowThatDoesNotParse(t *testing.T) {
	_, err := LoadFlows(fstest.MapFS{
		"flows/bent/010-x.toml": &fstest.MapFile{Data: []byte("id = \"x\"\nnonsense = 1\n")},
	}, "flows")
	if err == nil {
		t.Fatal("a flow with an unparseable stage loaded")
	}
	if !strings.Contains(err.Error(), "bent") {
		t.Errorf("the refusal does not name the flow: %v", err)
	}
}

// TestLoadFlowsRefusesAnUnreadableDirectory covers the ordinary filesystem
// failure, which has to name the directory rather than come back as an empty
// stock.
func TestLoadFlowsRefusesAnUnreadableDirectory(t *testing.T) {
	if _, err := LoadFlows(fstest.MapFS{}, "nowhere"); err == nil {
		t.Fatal("a directory that is not there loaded")
	}
}

// TestTheFallbackContractIsTheTasksOwn. An action that carried no flow used to
// fall back to the shipped one, which was the same value while a build ran one
// flow. With several it is a silent substitution: the stage ids overlap, so the
// wrong contract resolves to a real stage and the exit check asks for the wrong
// artifacts.
func TestTheFallbackContractIsTheTasksOwn(t *testing.T) {
	onFix := flowFallback(TaskState{FlowName: "fix"})
	fix, err := FlowNamed("fix")
	if err != nil {
		t.Fatalf("loading the fix flow: %v", err)
	}
	if Fingerprint(onFix) != Fingerprint(fix) {
		t.Error("a task on the fix flow fell back to a different contract")
	}

	// A state that names nothing is one no replay produced, and the default is the
	// only flow it could have been read against.
	if got := Fingerprint(flowFallback(TaskState{})); got != Fingerprint(DefaultFlow()) {
		t.Errorf("a state naming no flow fell back to %s", got)
	}

	// A name that does not resolve cannot come from a real log — the replay would
	// have failed first — so the default is the least-wrong answer available.
	if got := Fingerprint(flowFallback(TaskState{FlowName: "gone"})); got != Fingerprint(DefaultFlow()) {
		t.Errorf("a state naming a missing flow fell back to %s", got)
	}
}

// TestTheLoopCannotCloseOnAnOpinion is the property that replaced the split, and
// it is what makes the merged loop honest.
//
// `build`, `pipeline` and `verify` were separate stages so that the judging one
// could *require* what the command produced — the entry check then meant no model
// was ever paid to read a delivery the compiler had rejected. Merging them into
// `forge` gives that ordering back to the agent, which is a real loss and is
// stated as one in the stage file.
//
// What replaces it is narrower and holds where it matters most: the loop may not
// be *left* on a verdict a command contradicts. The stage declares what it
// converges on, the reducer refuses `converged` until that evidence passes, and
// the artifact named has to be proven by something that runs. A loop converging on
// prose would be a loop closing on an opinion.
func TestTheLoopCannotCloseOnAnOpinion(t *testing.T) {
	var looping []Stage
	for _, stage := range DefaultFlow() {
		if stage.Loop != nil {
			looping = append(looping, stage)
		}
	}
	if len(looping) == 0 {
		t.Fatal("no converging stage in the shipped flow — this check covers nothing")
	}

	for _, stage := range looping {
		if len(stage.Loop.ConvergesOn) == 0 {
			t.Errorf("stage %q loops and names nothing it converges on", stage.ID)
		}
		for _, artifact := range stage.Loop.ConvergesOn {
			if _, runs := VerifierFor(stage, artifact).(Command); !runs {
				t.Errorf("stage %q converges on %q, which nothing runs to prove — "+
					"the loop would close on an opinion", stage.ID, artifact)
			}
		}
	}
}

// TestTheLoopHasCeilings is INV-5 on the shipped flow: no infinite retry.
//
// A loop whose ceilings are all zero would take the defaults, which is fine — what
// must not happen is a ceiling that cannot be reached. The engine takes
// DefaultLoopLimits for a zero value, so this checks the resolved numbers rather
// than what the file happened to write.
func TestTheLoopHasCeilings(t *testing.T) {
	for _, stage := range DefaultFlow() {
		if stage.Loop == nil {
			continue
		}
		limits := stage.Loop.Limits
		if limits == (LoopLimits{}) {
			limits = DefaultLoopLimits()
		}
		if limits.MaxRounds < 1 || limits.NoProgress < 1 || limits.Oscillation < 1 {
			t.Errorf("stage %q loops with a ceiling nothing reaches: %+v", stage.ID, limits)
		}
	}
}

// TestAProjectCannotOverrideAFlow is the property that replaced the override.
//
// `luna init` used to copy the stock into a repository, and from then on the copy
// was what ran. It went because the thing it enabled is the thing that produces
// drift: two projects on the same Luna running different contracts, with nothing
// saying so. The binary is the definition now — one build, one set of flows — and
// a project that needs a different shape gets a new named flow in the stock, where
// everybody can see it.
func TestAProjectCannotOverrideAFlow(t *testing.T) {
	// Every name resolves to the embedded stock, and there is no seam to inject
	// through: no exported setter, and no unexported one either.
	for _, name := range FlowNames() {
		flow, err := FlowNamed(name)
		if err != nil {
			t.Fatalf("flow %q does not load: %v", name, err)
		}
		if len(flow) == 0 {
			t.Errorf("flow %q loaded empty", name)
		}
	}

	// And the set is exactly what the stock ships — a name nobody embedded is a
	// name nobody can run.
	if _, err := FlowNamed("a-flow-nobody-embedded"); err == nil {
		t.Error("a flow outside the embedded stock resolved")
	}
}
