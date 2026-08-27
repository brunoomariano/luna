// Package stock is what Luna ships with: the flows and the profiles.
//
// The files are embedded in the binary rather than read from beside it, so a
// Luna moved to another machine still has a flow. There is no project override
// and no copy on disk: one build, one set of flows, every repository the same.
// Changing a flow for everybody is an edit here and a rebuild, which keeps a
// flow where a flow belongs — in version control, under review, changed
// atomically. See flow.go for why the `.luna/stock/` override was removed.
package stock

import "embed"

// Files is the shipped stock, as an fs.FS.
//
// The stage loader takes an fs.FS rather than a path precisely so that this and
// a project's directory on disk are the same thing to it.
//
//go:embed flows/*/*.toml profiles/*.toml
var Files embed.FS

// Where each kind of stock lives inside Files.
//
// Flows are a directory of directories: `flows/<name>/` holds one flow's stages,
// and the directory name is the flow's name. A flow is self-contained rather than
// a selection over a shared pool of stages, because a lighter flow does not just
// drop stages — it rewires what the surviving ones require. `build` asks for
// `contract` under `full` and for `root_cause` under `fix`, and those are two
// different files rather than one file with a condition in it.
const (
	FlowsDir    = "flows"
	ProfilesDir = "profiles"
)
