package node

import (
	"os"
	"strings"
)

// uidMapPath is where the kernel reports this process's user-namespace mapping.
// A variable so a test can point it at a file rather than needing a sandbox.
var uidMapPath = "/proc/self/uid_map"

// Contained reports whether this process is running inside a user namespace —
// which is what ai-jail, bwrap and container runtimes all create.
//
// It exists because an agent that has to ask a person before each command cannot
// run unattended, and the flag that stops it asking is `bypassPermissions`. That
// flag is only defensible with something else doing the containing: ai-jail's own
// configuration states the rule as "flags perigosas DENTRO do jail, restrições do
// SO FORA", and INV-core-7 says the same thing from Luna's side — confining a
// process is a sandbox's job, and Luna delegates it rather than reimplementing it.
//
// The signal is the uid map, measured rather than assumed. Neither ai-jail 1.17.0
// nor bwrap exports an environment variable saying so, and an env var would be
// the wrong mechanism anyway: anything the agent can set, the agent can forge,
// and this decides how much the agent is trusted with.
//
//	host:  "         0          0 4294967295"   identity map, the whole range
//	jail:  "      1000          0          1"   one uid, remapped
//
// The first column is the uid inside the namespace and the second is the uid
// outside. Equal, from zero, over the full range means no remapping happened. A
// mapping that starts anywhere else means the process sees a different uid than
// the kernel does, which only a user namespace produces.
//
// A file that cannot be read answers false, and that is the safe direction: it
// says "not contained", which withholds the dangerous flag rather than granting
// it on a guess. Non-Linux hosts land here too.
func Contained() bool {
	raw, err := os.ReadFile(uidMapPath)
	if err != nil {
		return false
	}
	return remapped(string(raw))
}

// remapped reports whether a uid_map describes anything other than the identity
// mapping the host has.
//
// More than one line is already a remapping: the host's map is a single line, and
// a namespace mapping several ranges is still a namespace.
func remapped(uidMap string) bool {
	lines := strings.FieldsFunc(uidMap, func(r rune) bool { return r == '\n' })
	if len(lines) != 1 {
		return len(lines) > 1
	}

	fields := strings.Fields(lines[0])
	if len(fields) != 3 {
		return false
	}
	// The host maps uid 0 to uid 0 across the entire range. Anything else is a
	// namespace: a different starting uid, or a range that does not cover it all.
	//
	// Written as "is it the host's map, negated" rather than as the inverted
	// comparison De Morgan's law would give, because the identity map is the thing
	// worth naming — the inverted form reads as three unrelated inequalities.
	identity := fields[0] == "0" && fields[1] == "0" && fields[2] == "4294967295"
	return !identity
}
