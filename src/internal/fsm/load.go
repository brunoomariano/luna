package fsm

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// LoadFlow reads a flow from a directory of stage files.
//
// One file per stage, ordered by filename — `010-discovery.toml`,
// `020-setup.toml`. Order is significant: `AuditContract` checks precedence
// rather than existence, so which stage comes first is part of the contract. A
// numeric prefix makes inserting a stage an edit to one filename instead of a
// renumbering (RFC-0003).
//
// It takes an fs.FS rather than a path because the shipped flow is embedded in
// the binary and a project's copy is on disk, and neither should have to know
// which the other is.
func LoadFlow(files fs.FS, dir string) ([]Stage, error) {
	entries, err := fs.ReadDir(files, dir)
	if err != nil {
		return nil, fmt.Errorf("reading the stage directory %s: %w", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		names = append(names, e.Name())
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no stage files in %s: a flow with no stages is not a flow", dir)
	}
	// By name, which is why the names carry a numeric prefix. fs.ReadDir already
	// sorts, and sorting again says the order is meant rather than inherited.
	sort.Strings(names)

	stages := make([]Stage, 0, len(names))
	for _, name := range names {
		content, err := fs.ReadFile(files, path.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}

		stage, err := ParseStage(string(content), name)
		if err != nil {
			return nil, err
		}
		stages = append(stages, stage)
	}
	return stages, nil
}

// ParseStage reads one stage file.
//
// It refuses rather than guesses, in every direction: an unknown key, an unknown
// condition, an artifact with no declared verifier. A flow that half-loads is
// worse than one that will not load, because the half that is missing is
// invisible until a task walks into it.
func ParseStage(content, where string) (Stage, error) {
	var (
		stage   Stage
		section string
		gate    GateSpec
		review  ReviewSpec
		verify  = map[Artifact]Verifier{}
		partial = map[Artifact]*verifierSpec{}
	)

	for number, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(stripComment(raw))
		if line == "" {
			continue
		}
		at := fmt.Sprintf("%s:%d", where, number+1)

		if header, ok := sectionName(line); ok {
			section = header
			if artifact, ok := verifySection(header); ok {
				partial[artifact] = &verifierSpec{}
			}
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			return Stage{}, fmt.Errorf("%s: expected key = value, got %q", at, line)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)

		if err := assignStage(&stage, &gate, &review, partial, section, key, value, at); err != nil {
			return Stage{}, err
		}
	}

	for artifact, spec := range partial {
		verifier, err := spec.build(artifact, where)
		if err != nil {
			return Stage{}, err
		}
		verify[artifact] = verifier
	}

	return finish(stage, gate, review, verify, where)
}

// verifierSpec is a `[verify.<artifact>]` block before it becomes a Verifier.
//
// It is collected first and built afterwards because a block's meaning depends
// on which keys it carries, and TOML delivers them one line at a time.
type verifierSpec struct {
	kind  string
	run   string
	scope string
}

// build turns the collected keys into a verifier, refusing anything ambiguous.
func (s *verifierSpec) build(artifact Artifact, where string) (Verifier, error) {
	switch {
	case s.run != "" && s.kind != "":
		return nil, fmt.Errorf("%s: %s declares both a command and kind=%q — a verifier is one or the other",
			where, artifact, s.kind)

	case s.run != "":
		if s.scope == "" {
			// The scope is what stops a targeted run being read as a full one
			// later (ADR-0032). A command with no scope has not said what it
			// proves, and guessing would be the laundering the scopes prevent.
			return nil, fmt.Errorf("%s: %s runs a command and declares no scope (full, targeted, existence, human)",
				where, artifact)
		}
		scope, err := ParseScope(s.scope)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", where, artifact, err)
		}
		return Command{Run: s.run, Scope: scope}, nil

	case s.kind == "existence":
		return Existence{}, nil

	case s.kind != "":
		return nil, fmt.Errorf("%s: %s declares kind=%q; the only kind that runs nothing is `existence`",
			where, artifact, s.kind)

	default:
		return nil, fmt.Errorf("%s: %s declares a verifier with neither `run` nor `kind`", where, artifact)
	}
}

// assignStage puts one key into the stage being built.
func assignStage(
	stage *Stage, gate *GateSpec, review *ReviewSpec,
	verify map[Artifact]*verifierSpec,
	section, key, value, at string,
) error {
	if artifact, ok := verifySection(section); ok {
		return assignVerify(verify[artifact], key, value, at)
	}

	switch section {
	case "":
		return assignStageField(stage, key, value, at)
	case "gate":
		return assignGate(gate, key, value, at)
	case "review":
		return assignReview(review, key, value, at)
	default:
		return fmt.Errorf("%s: unknown section [%s]", at, section)
	}
}

func assignStageField(stage *Stage, key, value, at string) error {
	switch key {
	case "id":
		stage.ID = StageID(unquote(value))
	case "role":
		stage.Role = unquote(value)
	case "when":
		condition, err := ParseCondition(unquote(value))
		if err != nil {
			return fmt.Errorf("%s: %w", at, err)
		}
		stage.When = condition
	case "requires", "produces", "produces_for_human":
		return assignArtifactList(stage, key, value, at)
	default:
		return fmt.Errorf("%s: unknown key %q in a stage", at, key)
	}
	return nil
}

// assignArtifactList puts one of the three artifact lists into the stage.
//
// Split from the switch above because the three read identically and the parse
// is the same; keeping them inline made one function decide six things.
func assignArtifactList(stage *Stage, key, value, at string) error {
	list, err := parseArtifacts(value, at)
	if err != nil {
		return err
	}

	switch key {
	case "requires":
		stage.Requires = list
	case "produces":
		stage.Produces = list
	default:
		stage.ProducesForHuman = list
	}
	return nil
}

func assignGate(gate *GateSpec, key, value, at string) error {
	switch key {
	case "kind":
		kind, err := ParseGateKind(unquote(value))
		if err != nil {
			return fmt.Errorf("%s: %w", at, err)
		}
		gate.Kind = kind
	case "reason":
		gate.Reason = unquote(value)
	case "artifact":
		gate.Artifact = Artifact(unquote(value))
	default:
		return fmt.Errorf("%s: unknown key %q in [gate]", at, key)
	}
	return nil
}

func assignReview(review *ReviewSpec, key, value, at string) error {
	switch key {
	case "sends_back_to":
		review.SendsBackTo = StageID(unquote(value))
	case "invalidates":
		list, err := parseArtifacts(value, at)
		if err != nil {
			return err
		}
		review.Invalidates = list
	default:
		return fmt.Errorf("%s: unknown key %q in [review]", at, key)
	}
	return nil
}

func assignVerify(spec *verifierSpec, key, value, at string) error {
	if spec == nil {
		return fmt.Errorf("%s: a verify key outside any [verify.<artifact>] block", at)
	}

	switch key {
	case "run":
		spec.run = unquote(value)
	case "scope":
		spec.scope = unquote(value)
	case "kind":
		spec.kind = unquote(value)
	default:
		return fmt.Errorf("%s: unknown key %q in a verify block", at, key)
	}
	return nil
}

// finish assembles the stage and checks what only the whole file can answer.
func finish(stage Stage, gate GateSpec, review ReviewSpec, verify map[Artifact]Verifier, where string) (Stage, error) {
	if stage.ID == "" {
		return Stage{}, fmt.Errorf("%s: the stage declares no id", where)
	}
	if gate != (GateSpec{}) {
		stage.Gate = &gate
	}
	if review.SendsBackTo != "" || len(review.Invalidates) > 0 {
		stage.Review = &review
	}
	if len(verify) > 0 {
		stage.Verifiers = verify
	}

	// Every artifact the flow consumes declares how it is proven. `Existence` is
	// still available and is now a choice someone wrote down, which is what
	// ADR-0032 asked for and what a default nobody noticed could never be
	// (RFC-0003).
	//
	// ProducesForHuman is exempt: it is read by a person, and requiring a
	// verifier for a report would be requiring a machine check on prose.
	for _, artifact := range stage.Produces {
		if _, ok := verify[artifact]; !ok {
			return Stage{}, fmt.Errorf(
				"%s: %s produces %s and does not say how it is proven — add [verify.%s] "+
					"with a command, or kind = \"existence\" to say nothing checks it",
				where, stage.ID, artifact, artifact,
			)
		}
	}
	return stage, nil
}

// verifySection reports whether a header opens a verifier block, and for which
// artifact.
func verifySection(header string) (Artifact, bool) {
	rest, ok := strings.CutPrefix(header, "verify.")
	if !ok || rest == "" {
		return "", false
	}
	return Artifact(rest), true
}

// sectionName reads a `[section]` header.
func sectionName(line string) (string, bool) {
	if !strings.HasPrefix(line, "[") || !strings.HasSuffix(line, "]") {
		return "", false
	}
	return strings.TrimSpace(line[1 : len(line)-1]), true
}

// parseArtifacts reads a `["a", "b"]` list.
func parseArtifacts(value, at string) ([]Artifact, error) {
	inner, ok := strings.CutPrefix(strings.TrimSpace(value), "[")
	if !ok {
		return nil, fmt.Errorf("%s: expected a list like [\"a\", \"b\"], got %q", at, value)
	}
	inner, ok = strings.CutSuffix(strings.TrimSpace(inner), "]")
	if !ok {
		return nil, fmt.Errorf("%s: a list that opens with [ has to close with ]", at)
	}

	var list []Artifact
	for _, item := range strings.Split(inner, ",") {
		item = unquote(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		list = append(list, Artifact(item))
	}
	return list, nil
}

// unquote strips the quotes TOML puts around a string.
func unquote(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		return value[1 : len(value)-1]
	}
	return value
}

// stripComment removes a trailing `#` comment.
//
// It respects quotes, because a command is a string and `git log --format=%h #`
// is a legitimate thing to write. Naive splitting on `#` would silently truncate
// the command and leave a verifier proving something other than what was
// declared.
func stripComment(line string) string {
	var quoted bool
	for i, r := range line {
		switch {
		case r == '"':
			quoted = !quoted
		case r == '#' && !quoted:
			return line[:i]
		}
	}
	return line
}
