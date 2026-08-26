// Package lead conducts a task through the flow, in either of two shapes.
//
// Lead is the loop the design calls hybrid: code decides the next
// stage, calls the node, checks the delivery and records the transition —
// deterministic, zero tokens. When something goes off the rails, a model decides
// what to do with it, and that judgement arrives through the Judge interface
// rather than being wired in here. It is what `luna run` uses, and it needs no
// model at all.
//
// Agent is the same task conducted by a model, so a person can talk to the thing
// running it. What does not change is who decides the stage: the
// agent is handed one closed order at a time and its answer is never parsed, so
// there is no path from anything it says to a transition. It is what `luna lead`
// uses.
//
// The lead owns no state. Everything it knows it read from the store, and
// everything it decides it writes back before acting on it — so a process killed
// mid-task loses nothing but the work in flight.
package lead

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/store"
)

// ErrStalled is returned when a task stops making progress without failing. It is
// the failure mode the design calls the most expensive one: a fleet that stops
// talking stops in silence.
var ErrStalled = errors.New("the task stopped making progress")

// ErrInfrastructure is returned when the machinery around the task broke rather
// than the work in it — git is not installed, the agent harness is not on the
// machine, a worktree vanished.
//
// It is kept apart from an ordinary failure because the retry budget is for a
// stage that failed, and infrastructure says nothing about the stage.
// Retrying it would also be retrying the wrong thing: a binary that is not
// installed will not be installed on the second attempt either.
//
// The node layer wraps whatever its own transport reported, so the lead learns the
// distinction without importing the transport.
var ErrInfrastructure = errors.New("the machinery around the task broke")

// Node runs one stage and reports what came back.
//
// This is the boundary between the engine and the world. The implementation
// arrives with wave 5; until then the interface is what lets the lead be built
// and tested without a network, a process, or an agent.
type Node interface {
	// Run executes the stage and returns what it delivered, with the evidence for
	// each artifact. The evidence comes from running the real tool — the test
	// passed, the file exists, the commit resolves.
	Run(ctx context.Context, state fsm.TaskState, stage fsm.Stage) (Result, error)
}

// Result is what a node reports. It is deliberately not a fsm.Action: turning it
// into one is the lead's job, and keeping them apart means a node cannot decide a
// transition.
type Result struct {
	Delivered []fsm.Artifact
	Evidence  map[fsm.Artifact]fsm.Evidence

	// Guarded is the guarded paths this delivery touched, or nothing. It travels
	// with the evidence for the same reason: the node observes it, and the reducer
	// decides what it means without going and looking.
	Guarded []string

	// Spent is what the stage's agent cost, as its harness reported it. Zero for
	// a mechanical stage, which runs no agent.
	Spent fsm.Spend

	// Commit is what the stage delivered, and it is the handoff: the next stage
	// branches from it.
	//
	// Empty means the stage committed nothing, and then the base does not move —
	// which is right for a mechanical stage, and is a stage that produced nothing
	// durable for any other. It is not an error here: the contract check decides
	// whether the stage closes, and this only decides where the next one starts.
	Commit string
}

// Decision is what the model chose to do about a failure.
type Decision string

const (
	// DecideRetry runs the stage again with the error in context.
	DecideRetry Decision = "retry"

	// DecideBlock stops and notifies. Every blocked task notifies.
	DecideBlock Decision = "block"
)

// Judge is the judgement layer of the hybrid lead. It is consulted only when
// something has already gone wrong — never on the happy path, where a model in
// the loop would cost tokens and determinism for nothing.
//
// The reason it exists as an interface rather than a call to an LLM is
// testability, but there is a second reason worth naming: the design's own
// argument for a hybrid lead comes from a case where the machine was wrong and
// the model caught it. A judgement layer that cannot be swapped cannot
// be studied.
type Judge interface {
	OnFailure(ctx context.Context, state fsm.TaskState, reason string) Decision
}

// Lead conducts one task. One per task, never shared: the parallelism is between
// tasks, not inside them.
// There is no watchdog field, and the absence is deliberate. The original design
// imagined one polling the state between transitions; that was replaced with delegation,
// and delegation is what shipped — the node bounds the wait and a stall arrives as
// ErrStalled from Node.Run, which the loop below already handles. A second
// interface asking a replayed TaskState whether it looks stuck could only answer
// from a clock the state does not carry, which is why nothing but a test fake ever
// implemented it.
type Lead struct {
	Store *store.Store
	Node  Node
	Judge Judge

	// Flow defaults to the shipped one when empty.
	Flow []fsm.Stage

	// CheckGate runs the commands a person declared as the answer to one gate,
	// over what the task delivered, and reports what they concluded.
	//
	// A function rather than an interface pair because it is the seam between two
	// things the lead deliberately does not own: which commands answer a gate
	// lives in the registry, and running them belongs to the node layer. The lead
	// only needs the verdict, which is the pure-reducer shape — the observation
	// arrives, the decision is taken here.
	//
	// Nil means no mechanical half at all, which is what a run with no registry
	// configured has. Then every gate is decided by the judgement half alone,
	// which is exactly today's behaviour.
	CheckGate func(ctx context.Context, taskID string, gate fsm.GateKind) fsm.GateChecksOutcome

	// Ask is how the lead judges a gate the knob reached. It is the same boundary
	// Agent uses, and the same one drawn everywhere: Luna hosts no model of its own.
	//
	// Nil is the ordinary case — `luna run` needs no model, and a run with none
	// sends every judgement to a person rather than approving what nobody looked
	// at.
	Ask func(ctx context.Context, prompt string) (string, error)

	// Artifact reads a handed-over artifact back, for a gate that asks the lead to
	// judge one.
	//
	// The gate's payload carries the evidence line naming it — "handed over to
	// Luna, 9e727…" — which is right for an audit record and impossible to review:
	// a hash contains no obligations, so a lead asked to judge one declines, every
	// time. Measured on TALLY-5. `luna gate show` fetches the body for a person
	// with a comment saying exactly this; neither reader is served by the hash.
	//
	// A function rather than the store itself, for the reason the rest of this
	// struct already draws: the lead does not own where artifacts live. Nil leaves
	// the payload as it came, which is what a gate with nothing in the store has.
	Artifact func(taskID, artifact string) (body string, found bool)

	// Land points the task's branch at the commit it ended on.
	//
	// A function rather than a git call here for the reason the reducer's purity
	// already established: the lead decides *that* a task landed, and the node
	// layer is what touches a filesystem. Nil skips it, which is what a test with
	// no repository has.
	Land func(ctx context.Context, taskID, commit string) error

	// Warn reports what went wrong without failing the task. Nil discards.
	Warn func(format string, args ...any)
}

// warn reports a problem that is worth saying and not worth failing over.
func (l *Lead) warn(format string, args ...any) {
	if l.Warn != nil {
		l.Warn(format, args...)
	}
}

// Run drives a task until it needs a person or reaches the end.
//
// It returns the state it stopped at rather than an error for the ordinary
// endings: done, waiting at a gate, and blocked are all outcomes, not failures.
// An error means the lead itself could not continue — the store would not answer,
// or the log will not replay.
func (l *Lead) Run(ctx context.Context, taskID string) (fsm.TaskState, error) {
	flow := l.flow()

	for {
		state, err := l.Store.Replay(taskID, flow)
		if err != nil {
			return fsm.TaskState{}, err
		}

		// Three endings, none of them the lead's to push past. A gate is waiting on
		// a person, a block is waiting on a person, and done is done.
		if state.IsTerminal() || state.NeedsHuman() {
			l.land(ctx, state)
			return state, nil
		}

		// Cancellation is checked before doing anything rather than after: the point
		// of stopping is not to start the next thing.
		if err := ctx.Err(); err != nil {
			return state, err
		}

		if err := l.step(ctx, taskID, state, flow); err != nil {
			return fsm.TaskState{}, err
		}
	}
}

// PointBranchIfDone lands a finished task, for a caller driving its own loop.
//
// Exported because two loops end a task and both have to do this. `Lead.Run`
// drives `luna run` and calls the unexported one; `conductTask` drives `luna
// lead` and had no way in — it finished six stages without ever pointing the
// branch, while the status printed `luna/TALLY-8/done` for a ref nothing had
// created. Measured on TALLY-8, one commit after a fix that wired the Land field
// on that path and left nothing calling it.
//
// A task that is not done is not landed, so a caller may hand any ending state
// to it.
func (l *Lead) PointBranchIfDone(ctx context.Context, state fsm.TaskState) {
	l.land(ctx, state)
}

// land points the task's branch at what it delivered, so `done` means what
// it means: ready to integrate, on a branch a person can name.
//
// It runs on the way out of the loop rather than at the last stage, because
// "the task is finished" is a property of the state and not of any one stage —
// and a task resumed after a block reaches the end through a different path.
//
// A failure to point the branch does not fail the task, and is reported rather
// than swallowed. The work is committed by then and the state records the
// commit; turning "it delivered" into "it failed" because a ref would not move
// would lose the more important of the two — the same reasoning the worktree
// cleanup already follows.
func (l *Lead) land(ctx context.Context, state fsm.TaskState) {
	if l.Land == nil || state.Status != fsm.StatusDone {
		return
	}
	if err := l.Land(ctx, state.ID, state.Base); err != nil {
		l.warn("%s finished but its branch was not moved: %v", state.ID, err)
	}
}

// Enter opens the stage the order names, answering any gate on the way in.
//
// It exists because `luna lead` had no way to do this and therefore could not
// conduct anything. The loop the lead is briefed on is `next` for the order, do
// the work, `done` to report — but `next` reads and changes nothing by design,
// so the task stayed `ready` and `done` answered "no running stage to finish",
// on the first stage, every time. Measured on TALLY-4.
//
// Entering is not the lead's decision and is not offered to it: a task that is
// not running has exactly one next step and the status says which. What *is* a
// decision is the gate on the way in, and that goes through the same knob-aware
// path `luna run` uses rather than a second copy of it — the reason this is a
// method on Lead and not a helper in the CLI.
func (l *Lead) Enter(ctx context.Context, taskID string) error {
	flow := l.flow()

	state, err := l.Store.Replay(taskID, flow)
	if err != nil {
		return err
	}
	// A gate is entered past only when somebody is authorised to answer it, and
	// decideGate is what asks: the knob decides, the declared checks run, and a
	// gate nobody may answer records `waited` and stays open.
	//
	// This used to refuse `awaiting_gate` outright, on the reasoning that a gate
	// waits for a person — true only when nobody else may answer. A review gate
	// opens on the way *out* of the stage that produced its artifact, so the task
	// sits at that status and the decision is taken on the next Advance. Refusing
	// it meant the knob could not reach the only gate the shipped flow has:
	// measured on TALLY-5 at knob 9 against a criticality-9 gate, where the loop
	// ended at "wait" every time. `luna run` never had this, because its own step
	// advances from any status that is not running.
	//
	// A block and a finished task are still endings: nothing authorises walking
	// past those.
	// An open gate is answered, not advanced past — the reducer refuses an Advance
	// while one is pending, and it is right to: walking past a gate is not the
	// same act as deciding it. Whether the lead may decide is decideGate's
	// question, and a `waited` answer leaves the gate exactly where it was.
	if state.Status == fsm.StatusAwaitingGate {
		// A gate that already carries a verdict was judged when it opened, and
		// judging it again is a second model call for an answer already in hand.
		if state.Gate != nil && state.Gate.Judged != "" {
			return nil
		}

		account := l.decideGate(ctx, state, state.Gate)
		if account.Decision == fsm.GateDecisionJudged {
			return l.record(taskID, fsm.GateApprove{})
		}
		// Anything short of an approval leaves the gate where it was, and what the
		// lead concluded is filed against it. This is the one position where the
		// account cannot ride on the action that opens the gate, because the gate
		// was already open before anybody judged: `luna lead` has the agent close
		// its own stage through `luna done`, which reaches the gate with nothing
		// decided. Without this the judgement is paid for and thrown away —
		// measured on TALLY-6.
		if account.Judgement != "" {
			return l.record(taskID, fsm.GateJudged{Gate: account})
		}
		return nil
	}

	// A block and a finished task are endings, and nothing authorises walking
	// past those.
	if state.Status != fsm.StatusReady && state.Status != fsm.StatusStageDone {
		return nil
	}

	return l.record(taskID, fsm.Advance{
		Flow: flow,
		Gate: l.decideGate(ctx, state, fsm.GateAhead(state, flow)),
	})
}

// step performs exactly one transition and records it. Splitting it out keeps Run
// a loop over outcomes rather than a loop with a body.
func (l *Lead) step(ctx context.Context, taskID string, state fsm.TaskState, flow []fsm.Stage) error {
	// Anything but a running node means the next move is to enter a stage — a task
	// that has not started, or one whose stage just closed. The status says which,
	// so the lead never has to infer it.
	if state.Status != fsm.StatusRunning {
		return l.record(taskID, fsm.Advance{
			Flow: flow,
			Gate: l.decideGate(ctx, state, fsm.GateAhead(state, flow)),
		})
	}

	stage := stageIn(flow, state.Stage)

	result, err := l.Node.Run(ctx, state, stage)
	if err != nil {
		// A stall is a decision, not a judgement call: the node observed that the
		// agent is alive and doing nothing, and there is nothing for a model to
		// weigh. It also must not spend the retry budget — that budget
		// is for a stage that failed, and a stall says nothing about the stage.
		if errors.Is(err, ErrStalled) {
			return l.stall(taskID, err.Error())
		}
		// Infrastructure blocks without consulting anyone and without spending the
		// budget: there is no judgement to make about a binary that is missing, and
		// the budget belongs to the stage.
		if errors.Is(err, ErrInfrastructure) {
			return l.record(taskID, fsm.Block{Reason: err.Error()})
		}
		return l.handleFailure(ctx, taskID, state, err.Error())
	}

	// The verdict came from outside; the reducer decides what it means.
	// A stage that delivered less than it promised will not close, and the lead
	// does not argue with that — it records the attempt and lets the next pass see
	// a blocked task.
	if err := l.record(taskID, fsm.Complete{
		Delivered: result.Delivered,
		Evidence:  result.Evidence,
		Commit:    result.Commit,
		Spent:     result.Spent,
		Guarded:   result.Guarded,
		Flow:      flow,
		// A review gate opens when the stage that produced its artifact closes,
		// so this is the action that reaches it and the decision is
		// owed here rather than on the next advance.
		Gate: l.decideGate(ctx, state, fsm.GateClosing(state, flow)),
	}); err != nil {
		return err
	}

	// Read back rather than reusing the state from before the Complete: the base
	// only moves on the path where the stage actually closed, and that is exactly
	// the signal the round is judged by.
	after, err := l.Store.Replay(taskID, flow)
	if err != nil {
		return err
	}
	return l.readReview(ctx, taskID, stage, after, result)
}

// readReview turns a review stage's report into a transition, when it carries
// one.
//
// This is what the review contract specified and nothing implemented: the reviewer produces
// a report like any other artifact, and **Luna reads it**. A `[BLOCKING]`
// finding sends the work back; anything else lets the flow carry on.
//
// The agent never emits the action, and that is the point. Handing the reviewer
// a `luna review-finding --aligned` would be the direct route and would put a
// transition in a model's hands — with nothing able to stop a false one, and
// nothing able to detect it. So the model reports and the code decides, which is
// Flow control out of the model, applied to the one place where letting it decide would look
// most reasonable: it has just finished forming an opinion, and acting on one is
// the obvious next step.
//
// A stage that is not a review reads nothing. A review whose report has no
// recognisable finding reads nothing either, which is the honest outcome — the
// report is in the context, and a person can see what was written.
func (l *Lead) readReview(
	ctx context.Context, taskID string, stage fsm.Stage, state fsm.TaskState, result Result,
) error {
	artifact, ok := fsm.ReviewedArtifact(stage)
	if !ok {
		return nil
	}

	findings := fsm.ReadReport(l.reportBody(taskID, artifact, result))

	// What the review found and is not sending back. A defect the change did not
	// introduce does not reopen the work — that would turn every task into an
	// audit of the repository — but it is still a defect somebody verified, and
	// leaving it to be discovered in the report is how it is never read.
	//
	// Said before the early return, so it is said whether or not anything blocks.
	l.reportFindings(taskID, findings)

	if !fsm.Blocks(findings) {
		return nil
	}

	return l.record(taskID, fsm.ReviewFinding{
		Aligned:  true,
		Summary:  summarise(findings),
		Progress: progressOf(state, result),
		Flow:     l.flow(),
		// What a spent ceiling means is the lead's to decide, from the history.
		// It is consulted only when a ceiling is actually reached; an
		// ordinary round leaves this absent and nothing is asked.
		Gate: l.decideCeiling(ctx, state),
	})
}

// reportBody is the review report itself, wherever it ended up.
//
// The store first, because a report handed over the socket is not in the
// evidence: what the detail carries there is `handed over to Luna, <hash>`, and
// reading findings out of a hash finds none. The evidence's detail is the
// fallback, for a report that was delivered some other way.
//
// Measured on TALLY-8. The critic tagged a real regression [BLOCKING] — a stray
// `--avg` silently corrupting the sum, which the change had introduced — the
// parser read it correctly when handed the body, and the flow finished `done`
// without sending anything back, because this read the hash line instead. It is
// the same gap the gate had and closed: a caller reading the evidence line where
// it needed the artifact.
func (l *Lead) reportBody(taskID string, artifact fsm.Artifact, result Result) string {
	if l.Artifact != nil {
		if body, found := l.Artifact(taskID, string(artifact)); found {
			return body
		}
	}
	return result.Evidence[artifact].Detail
}

// reportFindings puts what the review found in front of a person.
//
// Only the ones that do not send work back, because the blocking ones already
// reopen the stage and are read there. These are the other outcome: a real
// defect, verified against a named input, that this change did not cause — on
// TALLY-7 there were four, every one of them true, and the only place any of
// them existed was a report nobody was told to open.
func (l *Lead) reportFindings(taskID string, findings []fsm.Finding) {
	for _, finding := range findings {
		if finding.Severity == fsm.SeverityBlocking {
			continue
		}
		l.warn("%s review [%s] %s", taskID, finding.Severity, finding.Text)
	}
}

// decideCeiling is what the lead does about a loop that stopped converging:
// block the task, or put it in front of a person.
//
// Not a fixed rule, because the two endings are right in different situations and
// only the history separates them — a loop that produced nothing for three rounds
// is a block, while one converging slowly is a question worth asking. That is the
// The hybrid-lead carve-out exactly as written: the lead decides what to do about a
// failure, never which stage comes next.
//
// With no model, it blocks. An unattended run that cannot ask must not carry on
// looping, and the absent decision is what produces that.
func (l *Lead) decideCeiling(ctx context.Context, state fsm.TaskState) fsm.GateAccount {
	if l.Ask == nil {
		return fsm.GateAccount{Decision: fsm.GateDecisionAbsent}
	}

	said, err := l.Ask(ctx, CeilingBrief(state.Loop))
	if err != nil {
		// The model could not be reached. Blocking is the conservative reading:
		// it stops and notifies rather than spending another round on a loop
		// nobody assessed.
		return fsm.GateAccount{Decision: fsm.GateDecisionAbsent, Judgement: "could-not-ask"}
	}

	account := fsm.GateAccount{Judgement: ReadCeiling(said).String(), Excerpt: excerpt(said)}
	if ReadCeiling(said) == CeilingAsk {
		account.Decision = fsm.GateDecisionWaited
		return account
	}
	account.Decision = fsm.GateDecisionAbsent
	return account
}

// progressOf is what this round produced, for the next one to compare against
// (PRD node-0002).
//
// It is the delivered commit. That is the closest thing Luna has to "did the
// work change", and it is exact rather than approximate: the handoff *is* the
// commit, so two rounds delivering the same sha delivered the same
// work — no hashing, no diff, no guessing which parts of a diff are meaningful.
//
// The PRD's open question asked what to hash, listing artifacts, the worktree
// diff and the evidence, and worried about meaningless variation — a timestamp
// in a diff that never matches itself. The commit sidesteps that entirely, and
// only because the handoff moved to git first: hashing a worktree
// would have had exactly the problem the question describes.
//
// A round that delivered no commit says nothing rather than guessing, which the
// reducer reads as "no comparison available".
func progressOf(state fsm.TaskState, result Result) string {
	if state.Base != "" {
		return state.Base
	}
	// Nothing was committed. The evidence is what the tools reported, and an
	// empty signal is the honest answer — a detector that invents one fires on
	// its own invention.
	_ = result
	return ""
}

// summarise names the blocking findings, so the log says what sent the work back
// rather than that something did.
func summarise(findings []fsm.Finding) string {
	var blocking []string
	for _, f := range findings {
		if f.Severity != fsm.SeverityBlocking {
			continue
		}
		if f.ID != "" {
			blocking = append(blocking, f.ID+": "+f.Text)
			continue
		}
		blocking = append(blocking, f.Text)
	}
	return strings.Join(blocking, "; ")
}

// flow is what this lead runs, defaulting to the shipped one.
func (l *Lead) flow() []fsm.Stage {
	if len(l.Flow) == 0 {
		return fsm.DefaultFlow()
	}
	return l.Flow
}

// decideGate works out who answers a gate, and turns that into the value the log
// will carry.
//
// A stage that opens no gate records no decision: there was nothing to decide,
// and writing "passed" would claim a gate was reached that never was.
//
// Whether it waits at all is no longer a profile's to say. A gate
// waits because the stage declared something to answer it with — judgement
// criteria in the flow, or checks in the task's registry entry. One with neither
// was never going to put a question in front of anybody, so stopping at it would
// be stopping to ask nothing.
func (l *Lead) decideGate(ctx context.Context, state fsm.TaskState, gate *fsm.PendingGate) fsm.GateAccount {
	if gate == nil {
		return fsm.GateAccount{Decision: fsm.GateDecisionAbsent}
	}

	// The two halves are declared in different places, so both are consulted
	// before concluding that nothing was: criteria live in the stage file and
	// checks live per task in the registry.
	spec := fsm.GateSpecIn(l.flow(), gate.Stage)

	checks := fsm.GateChecksOutcome{}
	if l.CheckGate != nil {
		checks = l.CheckGate(ctx, state.ID, gate.Kind)
	}

	if !declared(spec, checks) {
		return fsm.GateAccount{Decision: fsm.GateDecisionPassed}
	}
	return l.answerDeclaredGate(ctx, state, spec, gate, checks)
}

// declared reports whether anything was declared to answer this gate with.
//
// An unrunnable check counts as declared, and the distinction matters: it means
// somebody wrote commands that could not be run, which is a question for a person
// rather than a reason to carry on as though the gate were empty.
func declared(spec *fsm.GateSpec, checks fsm.GateChecksOutcome) bool {
	if checks.Passed || checks.Rejected || checks.Unrunnable {
		return true
	}
	return spec != nil && len(spec.Judge) > 0
}

// answerDeclaredGate is who answers a gate that had something declared for it.
//
// The checks arrive already run: the caller needed them to know whether anything
// was declared at all, and running them twice would double the cost of every
// gate — `make ci` is not a question to ask twice.
//
// Reading the registry and running commands is the node layer's work, and the
// verdict arrives as an observation the way every other one does.
// What this owns is the decision made from it, which is why the rule itself
// lives in fsm.ResolveGate and is testable without a registry or a shell.
func (l *Lead) answerDeclaredGate(
	ctx context.Context, state fsm.TaskState,
	spec *fsm.GateSpec, gate *fsm.PendingGate, checks fsm.GateChecksOutcome,
) fsm.GateAccount {
	switch fsm.ResolveGate(spec, checks, state.Knob) {
	case fsm.AnswerChecks:
		return fsm.GateAccount{Decision: fsm.GateDecisionChecked}
	case fsm.AnswerLead:
		return l.judge(ctx, state, spec, gate)
	case fsm.AnswerRejected, fsm.AnswerPerson:
		return fsm.GateAccount{Decision: fsm.GateDecisionWaited}
	default:
		// A resolution this build does not recognise falls to a person, which is
		// the direction every uncertain path in this design takes.
		return fsm.GateAccount{Decision: fsm.GateDecisionWaited}
	}
}

// judge asks the lead to answer a gate against its declared criteria.
//
// The knob having reached this gate is permission to judge, not a judgement:
// recording `judged` without anyone having judged would put a fact in the log
// that nothing produced, which is the worst of both designs — a model's authority
// with no model involved.
//
// Everything that is not a clear approval falls to a person. A rejection does
// too, deliberately: the reducer answers a gate at the moment it opens, and there
// is no path from here to a rejection that sends work back. Recording a person's
// wait is honest about that — the gate is still open, and what the lead concluded
// belongs in front of whoever answers it.
//
// What it concluded rides back in the GateAccount, and the caller puts it where
// the position allows: inside the action that opens the gate when there is no
// gate yet, and as a GateJudged when the gate is already open. It used to be a
// GateJudged either way, and from here that could never land — judging happens
// while computing the decision that opens the gate, so the reducer refused every
// one. Measured on TALLY-6, where the lead found a real contradiction in a
// contract and the log recorded nothing.
func (l *Lead) judge(ctx context.Context, state fsm.TaskState, spec *fsm.GateSpec, gate *fsm.PendingGate) fsm.GateAccount {
	if l.Ask == nil {
		// The knob authorised a judgement and there is nobody to make it. Asking a
		// person is the only honest answer: the alternative is approving a gate
		// because no model was configured to look at it.
		return fsm.GateAccount{Decision: fsm.GateDecisionWaited}
	}

	said, err := l.Ask(ctx, JudgingBrief(spec, Evidence{
		Artifact: l.artifactFor(state.ID, gate),
		// The task's own words, so a criterion asking about the task has
		// something to check against rather than the artifact's account of it.
		Statement: state.Statement,
	}))
	if err != nil {
		// The account says a model was asked and could not answer, which is a
		// different fact from nobody having been asked — and the one a person
		// looking at this gate afterwards most needs.
		return fsm.GateAccount{
			Decision:  fsm.GateDecisionWaited,
			Judgement: "could-not-ask",
			Excerpt:   excerpt(err.Error()),
		}
	}

	judgement := ReadJudgement(said)
	account := fsm.GateAccount{
		Judgement: judgement.String(),
		Excerpt:   excerpt(said),
	}

	// The decision is settled here; where the account lands is the caller's, and
	// depends on whether a gate is open yet. Measured at knob 9, where the lead
	// judged, concluded `cannot-decide`, and left nothing behind but a warning on
	// a stream nobody reads at night.
	account.Decision = fsm.GateDecisionWaited
	if judgement == JudgedApprove {
		account.Decision = fsm.GateDecisionJudged
	}
	return account
}

// excerpt keeps the end of what the lead said.
//
// The end because that is where a conclusion is, on the same reasoning the
// harness's own diagnostics are cut — and cut at all because this goes into an
// append-only log a fleet writes to every night, and a verbatim transcript per
// gate is a cost that only shows up later. The verdict beside it is structured
// and kept whole, so what is truncated is the working and never the answer.
func excerpt(said string) string {
	said = strings.TrimSpace(said)
	const most = 400
	if len(said) <= most {
		return said
	}
	return "…" + said[len(said)-most:]
}

// artifactFor is what the lead is actually asked to judge.
//
// The stored body when there is one, and the gate's payload otherwise — which is
// what a `confirm` gate carries, and what a review gate carries before its
// artifact was handed over.
func (l *Lead) artifactFor(taskID string, gate *fsm.PendingGate) string {
	if l.Artifact == nil || gate.Artifact == "" {
		return gate.Payload
	}
	if body, found := l.Artifact(taskID, string(gate.Artifact)); found {
		return body
	}
	return gate.Payload
}

// stall records a task that stopped making progress.
//
// It is a block rather than a failure, and the distinction is load-bearing: a
// failure says the stage went wrong, a stall says nothing about the stage at all.
// Collapsing them would spend the retry budget on an agent that is not going to
// react, and would leave the audit unable to tell the two apart at exactly the
// moment someone needs to know which happened.
func (l *Lead) stall(taskID, reason string) error {
	return l.record(taskID, fsm.Block{Reason: reason})
}

// handleFailure asks the judge what to do and records the answer.
//
// The retry budget is the reducer's to spend: recording a Fail is what increments
// it, and what turns the third one into a block. The judge choosing retry does not
// override that — it only means the lead is not escalating early.
func (l *Lead) handleFailure(ctx context.Context, taskID string, state fsm.TaskState, reason string) error {
	decision := DecideBlock
	if l.Judge != nil {
		decision = l.Judge.OnFailure(ctx, state, reason)
	}

	switch decision {
	case DecideRetry:
		return l.record(taskID, fsm.Fail{Reason: reason})
	case DecideBlock:
		// One decision, one event. Spending the retry budget to reach a block would
		// leave three failures in the log where there was one choice to escalate,
		// and the history is the audit trail.
		return l.record(taskID, fsm.Block{Reason: reason})
	default:
		return fmt.Errorf("the judge returned an unknown decision %q", decision)
	}
}

// record appends a transition after checking the reducer accepts it.
//
// Checking first matters: an action the reducer would refuse must not reach the
// log, because a log that will not replay is worse than a task that refused to
// move.
func (l *Lead) record(taskID string, action fsm.Action) error {
	flow := l.flow()

	state, err := l.Store.Replay(taskID, flow)
	if err != nil {
		return err
	}
	if _, err := fsm.Reduce(state, action); err != nil {
		return fmt.Errorf("refusing to record %T on %s: %w", action, taskID, err)
	}

	// Conditional on the log still ending where it was read. The dry run above
	// validated this action against `state`, and between reading it and writing
	// there is a window: something landing in it means the decision was made
	// against a task that has since moved, and appending anyway would put two
	// decisions taken from one state into a log that cannot be repaired.
	return l.Store.AppendActionAt(taskID, state.Seq, action)
}

func stageIn(flow []fsm.Stage, id fsm.StageID) fsm.Stage {
	for _, s := range flow {
		if s.ID == id {
			return s
		}
	}
	return fsm.Stage{ID: id}
}
