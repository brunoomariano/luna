package fsm

import (
	"fmt"
	"sort"
	"sync"

	"github.com/brunoomariano/luna/src/stock"
)

// DefaultFlowName is the flow a task gets when nobody names one.
//
// The heaviest of the shipped flows, and that direction is deliberate: it is the
// same asymmetry ParseKnob draws, where an absent value resolves to the most
// supervised setting. A task opened without thinking about its flow gets every
// stage, so neither a typo nor an unset field can quietly strip the planning and
// the review off a piece of work that wanted them.
const DefaultFlowName = "full"

// FlowNamed returns the stages of one named flow.
//
// An unknown name is an error naming what was asked and what exists, because the
// alternative is a task silently opened against the default when somebody meant
// something else.
func FlowNamed(name string) ([]Stage, error) {
	flow, ok := mustShippedFlows()[name]
	if !ok {
		return nil, fmt.Errorf("no flow named %q: this build runs %v", name, FlowNames())
	}
	// A copy per call: the flow is a slice, and a caller that appended to it
	// would edit the shipped one for everybody else.
	return append([]Stage(nil), flow...), nil
}

// FlowNames is every flow this build can run.
func FlowNames() []string {
	shipped := mustShippedFlows()

	names := make([]string, 0, len(shipped))
	for name := range shipped {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DefaultFlow is the flow Luna ships with — the nine stages of
// docs/architecture.md.
//
// Which flow runs is a task's choice (`--flow`); what a flow contains is not
// anyone's, and a project does not edit one. What every stage declares is the
// same either way — what it requires and what it produces.
//
// Order is significant: AuditContract checks precedence, not existence.
//
// It reads the stock embedded in the binary rather than returning Go literals.
// The files are the source, so the flow under review and the flow Luna runs are
// the same thing rather than two descriptions of it.
//
// A stock that does not parse is a panic, and deliberately: it is embedded at
// build time, so a broken one is a broken binary rather than a bad input. Every
// test in the suite calls this, which is what makes the parser proven by the
// whole suite rather than by its own tests alone.
//
// A task that names a flow is replayed against *that* one; this is the answer for
// callers with no task in hand — `luna flow check`, and the tests.
func DefaultFlow() []Stage {
	// The error is discarded rather than handled, and that is safe for one reason
	// stated rather than assumed: DefaultFlowName resolves in the embedded stock,
	// which TestTheDefaultFlowIsTheHeaviest and every other test in this file walk.
	// Nothing can remove it: the embedded stock is the only source there is.
	flow, _ := FlowNamed(DefaultFlowName)
	return flow
}

// shippedFlows parses every embedded flow once.
//
// Cached because a flow is looked up on every command and inside loops that
// replay a task, and parsing the stage files each time would make the flow's cost
// grow with the log's length.
var shippedFlows = sync.OnceValues(func() (map[string][]Stage, error) {
	return LoadFlows(stock.Files, stock.FlowsDir)
})

// mustShippedFlows is the embedded stock, or a panic.
//
// A panic and not an error, in one place rather than at each caller: the stock is
// embedded at build time, so a stock that does not parse is a broken binary rather
// than a bad input. Every caller would otherwise propagate an error that no
// deployment can produce, and the branch for it is one no test can reach.
func mustShippedFlows() map[string][]Stage {
	flows, err := shippedFlows()
	if err != nil {
		panic(fmt.Sprintf("the embedded stock does not parse, which is a broken build: %v", err))
	}
	return flows
}

// The flows a build runs come from the embedded stock and nowhere else.
//
// There used to be a project override: `luna init` copied the stock into
// `.luna/stock/` and from then on the copy was what ran. It went because the
// thing it enabled — a project editing its own flow — is the thing that produces
// drift, and the binary already gives what the override was reaching for. One
// build, one set of flows, every repository the same. Changing a flow for
// everybody is an edit to `src/stock/flows/` and a rebuild, which keeps the flow
// where a flow belongs: in version control, under review, changed atomically.
//
// A project that genuinely needs a different shape gets a new named flow in the
// stock rather than a private copy of an existing one — the difference is that
// the first is visible to everyone and the second was visible to nobody.

// SoloRole is the role every stage runs under when one agent carries the whole
// task. It is the only role a solo run resolves, and the stock ships it.
const SoloRole = "lead"

// SoloAgent is the harness a solo run uses, and SoloBrief is what it is told.
//
// One brief covering every stage, where a pack gives each stage its own. That is
// the trade solo makes: the same agent does the planning, the building and the
// judging, so what it is told has to hold for all three at once rather than being
// written for the stage in front of it.
//
// It lived in `stock/roles/lead.toml` while roles were a table a stage pointed
// into. With the brief absorbed into the stages, a file holding one role that no
// stage names would be a table with a single row — so the row moved here, next to
// the function that is the only thing which ever read it.
const (
	SoloAgent = "claude"
	SoloBrief = "You carry out one stage at a time, and Luna decides which. Do what the stage says it owes and nothing further: a stage that delivers more than its contract asks has done work nobody can check. When what you owe is a contract, every sentence in it binds someone — an obligation, a prohibition, or a statement of fact a checker can settle. It carries no recommendations, no notes for later, and no section for them: a sentence saying one option is preferable is one a checker cannot act on, and it does not belong in the document. Where two forms are genuinely both acceptable, say that both satisfy the contract and stop. When you are asked to find a root cause, find the cause and the smallest case that shows it, and do not fix it in that stage. When you are asked to judge what was delivered, you are re-reading your own work with no memory of writing it, and that is worth saying to yourself: look for what the contract obliges that you cannot find satisfied, and treat an obligation you cannot locate as a finding whatever the suite says. Apply each lens the work admits and say what each found, including when it found nothing. correctness: does the code do what the scenarios and the contract say, including where those two disagree? coverage: what does the suite not cover — the case that would still pass if the behaviour were absent? robustness: what breaks under load, attack or absence — empty input, a failing dependency, a concurrent caller? structure: did the change move the system's shape, and does the shape still hold? A finding names the file and the line, what goes wrong, and the input that makes it go wrong; a finding you cannot state that way is an impression and belongs in the report as one. Every finding opens with exactly one tag on its own line, in square brackets, and Luna reads only the tag: [BLOCKING] this change introduced the defect, or it breaks a stated acceptance criterion — this and only this sends the work back; [SHOULD-FIX] a real defect this change did not introduce, or that no acceptance criterion covers; [NIT] a preference; [UNCERTAIN] you suspect a defect and cannot confirm it, and you say what would confirm it. An untagged finding is invisible to Luna, so a defect you describe without a tag is one you did not report. Do not reach for [BLOCKING] because a defect is serious: a serious defect that was already there is [SHOULD-FIX], and blocking on it reopens the work to fix something nobody asked about. Ask what this change did, not what the file deserves."
)

// Solo collapses a flow's roles onto one, for the mode where a single agent
// carries the task from end to end.
//
// The pack declares a role per specialism — planner, coder, auditor — and each
// one buys a worktree of its own, a session of its own, and the tool denials that
// make an audit independent. A solo run buys none of that and does not pretend
// to: one agent, one worktree, one session, and the `audit` stage re-reading its
// own work with no memory of writing it, which is the one half of independence a
// single agent can have.
//
// Safe to apply after a task has started, and that is not an accident of the
// implementation. `Role` is policy rather than history — the reducer never reads
// it, only the node does — so it is out of the flow fingerprint by the same rule
// that keeps gate criteria and verifier commands out. A task begun solo replays
// against a pack and the other way round.
//
// Mechanical stages keep their empty role: a stage that starts no agent has
// nobody to be.
func Solo(flow []Stage) []Stage {
	solo := make([]Stage, len(flow))
	copy(solo, flow)

	for i := range solo {
		if solo[i].Mechanical() {
			continue
		}
		solo[i].Role = SoloRole
		solo[i].Agent = SoloAgent
		solo[i].Brief = SoloBrief

		// The denials go with the briefs they belonged to. A solo run is one agent
		// doing every stage, so a stage that denied Edit would be denying it to
		// the same process that has to build — which is the independence solo
		// already says it does not have, rather than one it can keep by half.
		solo[i].ToolsDeny = nil
	}
	return solo
}
