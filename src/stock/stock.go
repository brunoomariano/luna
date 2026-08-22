// Package stock is what Luna ships with: the flows, the roles, the profiles.
//
// The files are embedded in the binary rather than read from beside it, so a
// Luna moved to another machine still has a flow. `luna init` writes a copy into
// a project's `.luna/stock/`, and from then on the copy is what runs — which is
// how the replaceable flow stops being a promise and becomes an edit.
package stock

import "embed"

// Files is the shipped stock, as an fs.FS.
//
// The stage loader takes an fs.FS rather than a path precisely so that this and
// a project's directory on disk are the same thing to it.
//
//go:embed flows/*/*.toml roles/*.toml profiles/*.toml
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
	RolesDir    = "roles"
	ProfilesDir = "profiles"
)
