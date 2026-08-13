package fsm

import (
	"fmt"
	"sync"

	"github.com/brunoomariano/luna/src/stock"
)

// DefaultFlow is the flow Luna ships with — the 14 stages of
// docs/architecture/stages.md.
//
// It is not mandatory: stages can be disabled, edited or replaced, and new ones
// created (ADR-0017). What does not change is the contract — every stage declares
// what it requires and what it produces.
//
// Order is significant: AuditContract checks precedence, not existence.
//
// It reads the stock embedded in the binary rather than returning Go literals
// (RFC-0003). The files are the source, so the surface a project edits and the
// flow Luna runs are the same thing rather than two descriptions of it — which
// is what makes ADR-0017 true rather than aspirational.
//
// A stock that does not parse is a panic, and deliberately: it is embedded at
// build time, so a broken one is a broken binary rather than a bad input. Every
// test in the suite calls this, which is what makes the parser proven by the
// whole suite rather than by its own tests alone.
func DefaultFlow() []Stage {
	if len(project) > 0 {
		return append([]Stage(nil), project...)
	}

	shipped, err := shippedFlow()
	if err != nil {
		panic(fmt.Sprintf("the embedded stock does not parse, which is a broken build: %v", err))
	}
	// A copy per call: the flow is a slice, and a caller that appended to it
	// would edit the shipped one for everybody else.
	return append([]Stage(nil), shipped...)
}

// shippedFlow parses the embedded stock once.
//
// Cached because DefaultFlow is called on every command and inside loops that
// replay a task, and parsing fourteen files each time would make the flow's cost
// grow with the log's length.
var shippedFlow = sync.OnceValues(func() ([]Stage, error) {
	return LoadFlow(stock.Files, stock.StagesDir)
})

// project is the flow a project defined, when it has one.
//
// A package-level value rather than a parameter threaded through fifteen call
// sites, and that is a trade worth naming: the flow is a property of the
// repository Luna is pointed at, and every one of those callers would be passing
// the same value down. What keeps it from being a mutable global is UseFlow —
// set once, at startup, before anything reads it.
var project []Stage

// UseFlow makes a project's own stages the flow Luna runs.
//
// Called once from main, after reading `.luna/stock/`, and before any command
// runs. A project without one never calls it and gets the embedded copy, which
// is what a repository that never ran `luna init` should get.
//
// It exists because the flow has to be decided where the repository is known,
// and that is the process boundary — not the engine, which must not read a
// filesystem (ADR-0024).
func UseFlow(flow []Stage) { project = flow }
