// Package stock is what Luna ships with: the stages, the roles, the profiles.
//
// The files are embedded in the binary rather than read from beside it, so a
// Luna moved to another machine still has a flow. `luna init` writes a copy into
// a project's `.luna/stock/`, and from then on the copy is what runs — which is
// how ADR-0017's replaceable flow stops being a promise and becomes an edit
// (RFC-0003).
package stock

import "embed"

// Files is the shipped stock, as an fs.FS.
//
// The stage loader takes an fs.FS rather than a path precisely so that this and
// a project's directory on disk are the same thing to it.
//
//go:embed stages/*.toml
var Files embed.FS

// StagesDir is where the stage files live inside Files.
const StagesDir = "stages"
