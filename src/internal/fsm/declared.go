package fsm

import "regexp"

// deliveredLine matches an agent's declaration of what it produced:
//
//	Delivered: dod_checked, ci_green
//
// Permissive about what surrounds it and strict about the shape, for the same
// reason findingLine is: the agent writes a commit message, and a commit message
// arrives with trailers, bullets and wrapping. What must not be loose is the
// keyword, because a line this fails to recognise silently becomes "declared
// nothing".
var deliveredLine = regexp.MustCompile(`(?mi)^[\s\-\*>]*(?:\*\*)?delivered(?:\*\*)?\s*:\s*(?:\*\*)?\s*(.+?)\s*(?:\*\*)?$`)

// artifactSeparator splits the list. Commas are what the brief asks for; spaces
// are what a hurried agent writes instead.
var artifactSeparator = regexp.MustCompile(`[,\s]+`)

// ReadDelivered pulls the artifacts an agent says it produced out of the text it
// left behind — its commit message, in practice.
//
// It exists because the node reported `Delivered: owed`: everything the stage
// *should* produce, regardless of what happened. The contract's exit check then
// compared the stage's promises against a copy of themselves and always agreed.
// Measured on a full run: `verify` owed `dod_checked`, committed a file called
// `verification`, and closed green.
//
// This does not verify anything, and must not be mistaken for it. It is the
// agent reporting, and the code deciding — the same shape as ReadReport
// (ADR-0041, INV-core-1). A stage that declares an artifact it did not produce
// still closes; what this catches is the far commoner case of an agent that
// produced something else and says so.
//
// Nothing recognisable yields nothing, and the caller falls back to what it did
// before. That direction is deliberate: a stage must not fail because its agent
// wrote a commit message in a shape this cannot read.
func ReadDelivered(report string) []Artifact {
	match := deliveredLine.FindStringSubmatch(report)
	if match == nil {
		return nil
	}

	var delivered []Artifact
	for _, name := range artifactSeparator.Split(match[1], -1) {
		if name == "" {
			continue
		}
		delivered = append(delivered, Artifact(name))
	}
	return delivered
}
