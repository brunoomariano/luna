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

// DefaultFlow is the flow Luna ships with — the eight stages of
// docs/architecture.md.
//
// It is not mandatory: stages can be disabled, edited or replaced, and new ones
// created. What does not change is the contract — every stage declares
// what it requires and what it produces.
//
// Order is significant: AuditContract checks precedence, not existence.
//
// It reads the stock embedded in the binary rather than returning Go literals.
// The files are the source, so the surface a project edits and the
// flow Luna runs are the same thing rather than two descriptions of it — which
// is what makes "a project brings its own flow" true rather than aspirational.
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
