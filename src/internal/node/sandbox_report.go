package node

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// The two files that decide what a task may touch and where its memory goes.
//
// Named here rather than discovered, because a report that quietly found nothing
// reads the same as one that found an empty configuration — and those are
// opposite answers to the question the gate asks.
const (
	jailConfig   = ".ai-jail"
	memoryConfig = ".ai-memory.toml"
)

// SetupReport is the artifact the sandbox summary is stored as.
//
// Named here rather than in the flow because the node has to recognise it: a
// mechanical stage cannot hand anything over, so Luna writes this one itself, and
// it needs to know which artifact it is being asked for.
const SetupReport fsm.Artifact = "setup_report"

// SandboxReport is what `setup` hands to the gate: what the sandbox will mount
// and which workstream the task writes to, read from the repository as it stands.
//
// Written by Luna rather than by an agent, and it has to be: `setup` is
// mechanical, so no agent runs and the handover socket is never opened. A
// contract asking a mechanical stage for a handed-over artifact is one nothing
// can satisfy — the shape this project has twice recorded as the worst kind of
// rule, because it reads like a guarantee and fires as a failure.
//
// It reports rather than repairs. A missing file is named as missing and the gate
// is where somebody decides about it: writing a default into a repository is a
// change nobody asked for, and doing it before the person has seen what is there
// is the opposite of what the gate is for.
func SandboxReport(repo string) string {
	var b strings.Builder

	b.WriteString("# Sandbox and memory for this task\n\n")
	writeConfig(&b, repo, jailConfig,
		"what the agent may read and write; without it the sandbox falls back to its own defaults")
	writeConfig(&b, repo, memoryConfig,
		"which workstream every agent of this task writes to; without it the scope is resolved from the path, which breaks under a worktree and inside the jail")

	b.WriteString("\nConfirm this is the sandbox and the workstream this task should run under.\n")
	return b.String()
}

// writeConfig renders one configuration file, or says plainly that it is absent.
func writeConfig(b *strings.Builder, repo, name, why string) {
	fmt.Fprintf(b, "## %s\n\n", name)

	body, err := os.ReadFile(filepath.Join(repo, name)) //nolint:gosec // a name this package fixed
	if err != nil {
		fmt.Fprintf(b, "Not present. %s\n\n", why)
		return
	}

	fmt.Fprintf(b, "```toml\n%s\n```\n\n", strings.TrimRight(string(body), "\n"))
}
