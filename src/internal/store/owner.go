package store

// ErrNotTheOwner is returned when something other than Luna opens the log for
// writing.
//
// The rule is swarm-forge's, arrived at the expensive way and enforced there
// with an exit code rather than a line in a prompt: the shared state has exactly
// one owner. Luna already applies it to the merge (ADR-0053); the log needs it
// for the same reason and more urgently, because an agent that appends to the
// log does not corrupt a file — it fabricates history, and history is the audit
// trail (INV-core-2).
var ErrNotTheOwner = errNotTheOwner{}

type errNotTheOwner struct{}

func (errNotTheOwner) Error() string {
	return "only Luna writes the log: an agent reports through `luna done` and Luna records it"
}

// Owner is who a store is opened on behalf of.
type Owner string

// LunaOwnsTheLog is the only value that may write.
//
// Set from the process that resolved the flow, never from configuration and
// never from the environment: an owner an agent can set is not an owner.
const LunaOwnsTheLog Owner = "luna"
