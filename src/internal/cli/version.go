package cli

import (
	"fmt"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// versionCommand says what this binary is and what surface it carries.
//
// It exists because a stale install is invisible until it refuses a flag. A
// reader following the skill against an older binary gets `unknown flag --flow`
// and no way to tell whether the skill is wrong or the binary is behind —
// measured on a real run, where the skill documented three flows and the
// installed binary carried one.
//
// So it prints two things and not one. The build is who this is; the flows and
// their fingerprints are what it can actually do, which is the half a skill or a
// runbook is written against.
func versionCommand(env Env, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%w: version takes no arguments", ErrUsage)
	}

	fmt.Fprintf(env.Out, "luna %s\n", buildVersion())

	for _, name := range shippedFlowNames() {
		flow := shippedFlow(name)
		fmt.Fprintf(env.Out, "  flow %s/%s (%d stages, pack of %d)\n",
			name, fsm.Fingerprint(flow), len(flow), len(packRoles(flow)))
	}
	return nil
}

// shippedFlowNames is every flow this build carries, in a stable order.
func shippedFlowNames() []string {
	names := fsm.FlowNames()
	sort.Strings(names)
	return names
}

// shippedFlow resolves a name that came from shippedFlowNames.
//
// The error is discarded rather than handled, and that is safe for one reason
// stated rather than assumed: both sides read the same embedded stock, so a name
// this build listed is a name this build resolves. Handling it would be a branch
// no test can reach, which is the kind of code that gets trusted without ever
// having held.
func shippedFlow(name string) []fsm.Stage {
	flow, _ := fsm.FlowNamed(name)
	return flow
}

// buildVersion reads what the toolchain stamped in.
//
// Thin on purpose: it reads the boundary and hands the answer to versionFrom,
// which is where the three shapes live. A test cannot vary how it was built, so
// the part worth testing is the part that does not know.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "(unknown build)"
	}
	return versionFrom(info.Main.Version, info.Settings)
}

// versionFrom turns what the toolchain stamped into what a person reads.
//
// `go install <module>@<version>` stamps the module version — including the
// `+dirty` the toolchain appends itself — and a local `go build` stamps the vcs
// revision instead. All three answers are honest for how the binary was produced;
// a number in a constant would be one more thing to forget to bump, and it would
// be wrong exactly when somebody was checking it.
func versionFrom(module string, settings []debug.BuildSetting) string {
	if module != "" && module != "(devel)" {
		return module
	}

	for _, setting := range settings {
		if setting.Key != "vcs.revision" {
			continue
		}
		if len(setting.Value) > 12 {
			return setting.Value[:12]
		}
		return setting.Value
	}
	return "(devel)"
}

// SurfaceStamp is what a skill or a runbook can be written against.
//
// The flow names and their fingerprints, joined — so a document that says which
// stamp it assumes can be checked against a binary in one comparison, rather than
// by discovering a missing flag at the first command.
func SurfaceStamp() string {
	names := shippedFlowNames()
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"/"+string(fsm.Fingerprint(shippedFlow(name))))
	}
	return strings.Join(parts, " ")
}
