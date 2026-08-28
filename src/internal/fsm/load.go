package fsm

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

// LoadFlows reads every flow under a directory of flow directories.
//
// `flows/full/`, `flows/fix/` — the directory name is the flow's name, so the
// name cannot disagree with its contents the way a `name =` field inside one of
// the stage files could.
//
// A directory holding no stage files is an error rather than an empty flow, and
// it comes back naming the directory: a flow that loads as nothing would pass
// every static check and then run a task through no stages at all.
func LoadFlows(files fs.FS, dir string) (map[string][]Stage, error) {
	entries, err := fs.ReadDir(files, dir)
	if err != nil {
		return nil, fmt.Errorf("reading the flow directory %s: %w", dir, err)
	}

	flows := map[string][]Stage{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		flow, err := LoadFlow(files, path.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("flow %q: %w", e.Name(), err)
		}
		flows[e.Name()] = flow
	}

	if len(flows) == 0 {
		return nil, fmt.Errorf("no flows in %s: a build with no flow can open no task", dir)
	}
	return flows, nil
}

// LoadFlow reads a flow from a directory of stage files.
//
// One file per stage, ordered by filename — `010-discovery.toml`,
// `020-setup.toml`. Order is significant: `AuditContract` checks precedence
// rather than existence, so which stage comes first is part of the contract. A
// numeric prefix makes inserting a stage an edit to one filename instead of a
// renumbering.
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
		blocks  = stageBlocks{partial: map[Artifact]*verifierSpec{}}
		verify  = map[Artifact]Verifier{}
	)

	lines := strings.Split(content, "\n")
	for number := 0; number < len(lines); number++ {
		line, consumed, err := readLine(lines, number, where)
		if err != nil {
			return Stage{}, err
		}
		at := fmt.Sprintf("%s:%d", where, number+1)
		number += consumed
		if line == "" {
			continue
		}

		if header, ok := sectionName(line); ok {
			section = header
			if artifact, ok := verifySection(header); ok {
				blocks.partial[artifact] = &verifierSpec{}
			}
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			return Stage{}, fmt.Errorf("%s: expected key = value, got %q", at, line)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)

		if err := assignStage(&stage, &blocks, section, key, value, at); err != nil {
			return Stage{}, err
		}
	}

	for artifact, spec := range blocks.partial {
		verifier, err := spec.build(artifact, where)
		if err != nil {
			return Stage{}, err
		}
		verify[artifact] = verifier
	}

	return finish(stage, blocks, verify, where)
}

// verifierSpec is a `[verify.<artifact>]` block before it becomes a Verifier.
//
// It is collected first and built afterwards because a block's meaning depends
// on which keys it carries, and TOML delivers them one line at a time.
type verifierSpec struct {
	kind     string
	run      string
	scope    string
	path     string
	handover string
}

// build turns the collected keys into a verifier, refusing anything ambiguous.
func (s *verifierSpec) build(artifact Artifact, where string) (Verifier, error) {
	handover, err := s.handedOver(artifact, where)
	if err != nil {
		return nil, err
	}

	switch {
	case s.path != "" && s.run != "":
		// A command already says what it proves by running. A path beside it would
		// be a second, weaker check on the same artifact, and the question "which
		// one decided?" has no good answer — the path is for the artifacts nothing
		// runs against.
		return nil, fmt.Errorf("%s: %s runs a command and declares a path — a command proves what a path would",
			where, artifact)

	case s.run != "" && s.kind != "":
		return nil, fmt.Errorf("%s: %s declares both a command and kind=%q — a verifier is one or the other",
			where, artifact, s.kind)

	case s.run != "":
		return s.buildCommand(artifact, where)

	case s.kind == "existence":
		return Existence{Path: s.path, Handover: handover}, nil

	case s.kind != "":
		return nil, fmt.Errorf("%s: %s declares kind=%q; the only kind that runs nothing is `existence`",
			where, artifact, s.kind)

	default:
		return nil, fmt.Errorf("%s: %s declares a verifier with neither `run` nor `kind`", where, artifact)
	}
}

// buildCommand turns a `run` declaration into a Command verifier.
func (s *verifierSpec) buildCommand(artifact Artifact, where string) (Verifier, error) {
	if s.scope == "" {
		// The scope is what stops a targeted run being read as a full one later.
		// A command with no scope has not said what it proves, and
		// guessing would be the laundering the scopes prevent.
		return nil, fmt.Errorf("%s: %s runs a command and declares no scope (full, targeted, existence, human)",
			where, artifact)
	}
	scope, err := ParseScope(s.scope)
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %w", where, artifact, err)
	}
	return Command{Run: s.run, Scope: scope}, nil
}

// storableAlone refuses the keys that contradict a store handover.
//
// An artifact handed to Luna is not in the commit at all, so a command run over
// the delivery has nothing to run against, and a path — where it lives *in the
// commit* — describes a place it will never be. Both are the same
// mistake as a path beside a command, one step further out.
func (s *verifierSpec) storableAlone(artifact Artifact, where string) error {
	if s.run != "" {
		return fmt.Errorf("%s: %s runs a command and is handed over to Luna — a command checks the commit, "+
			"and an artifact handed over is not in it", where, artifact)
	}
	if s.path != "" {
		return fmt.Errorf("%s: %s declares a path and is handed over to Luna — a path is where it lives "+
			"in the commit, and an artifact handed over is not committed", where, artifact)
	}
	return nil
}

// handedOver reads the `handover` key, which names where an artifact is handed
// over rather than whether it is checked.
//
// Only one destination is accepted. A free-form value would let a typo — `stoer`
// — parse as "not the store" and silently put the artifact back in the commit,
// which is the kind of quiet fallback this project refuses everywhere else.
func (s *verifierSpec) handedOver(artifact Artifact, where string) (bool, error) {
	switch s.handover {
	case "":
		return false, nil
	case "store":
		return true, s.storableAlone(artifact, where)
	default:
		return false, fmt.Errorf("%s: %s declares handover=%q; the only destination is \"store\"",
			where, artifact, s.handover)
	}
}

// assignStage puts one key into the stage being built.
// stageBlocks is every `[...]` section a stage file may open, collected while the
// file is read and assembled once at the end.
//
// A struct rather than five parameters threaded through the parser: the list grew
// with the loop, and a function taking ten arguments is one where a caller
// eventually passes two of them in the wrong order.
type stageBlocks struct {
	gate    GateSpec
	review  ReviewSpec
	loop    LoopSpec
	partial map[Artifact]*verifierSpec
}

func assignStage(stage *Stage, blocks *stageBlocks, section, key, value, at string) error {
	if artifact, ok := verifySection(section); ok {
		return assignVerify(blocks.partial[artifact], key, value, at)
	}

	switch section {
	case "":
		return assignStageField(stage, key, value, at)
	case "gate":
		return assignGate(&blocks.gate, key, value, at)
	case "review":
		return assignReview(&blocks.review, key, value, at)
	case "loop":
		return assignLoop(&blocks.loop, key, value, at)
	default:
		return fmt.Errorf("%s: unknown section [%s]", at, section)
	}
}

func assignStageField(stage *Stage, key, value, at string) error {
	switch key {
	case "id":
		stage.ID = StageID(unquote(value))
	case "agent", "brief", "skills", "tools_deny":
		return assignStageAgent(stage, key, value, at)
	case "when":
		condition, err := ParseCondition(unquote(value))
		if err != nil {
			return fmt.Errorf("%s: %w", at, err)
		}
		stage.When = condition
	case "context":
		context, err := ParseStageContext(unquote(value), at)
		if err != nil {
			return err
		}
		stage.Context = context
	case "role":
		// Refused rather than ignored, the same way `memory` is. A stage file still
		// carrying the key would read as configuration that groups something, and
		// nothing groups any more: the brief is the stage's own, the worktree is
		// named after the stage, and a stage is mechanical when it names no agent.
		return fmt.Errorf("%s: `role` is gone — a stage names its own `agent` and "+
			"`brief`, and a stage with neither is mechanical", at)
	case "memory":
		// Refused rather than ignored. Memory is the task's now — one workstream
		// for every agent a task starts — and a stage file still carrying the old
		// key would read as configuration that does something.
		return fmt.Errorf("%s: `memory` is a task's setting, not a stage's — "+
			"`luna task new --workstream <name>` picks the workstream every agent "+
			"of that task writes to", at)
	case "requires", "produces", "produces_for_human":
		return assignArtifactList(stage, key, value, at)
	default:
		return fmt.Errorf("%s: unknown key %q in a stage", at, key)
	}
	return nil
}

// assignStageAgent puts one of the four fields describing who runs the stage.
//
// Split from the switch above for the reason assignArtifactList was: four keys
// inline pushed one function past the complexity the linter gates on, and what
// they have in common — they describe the agent rather than the contract — is
// worth saying with a function name.
func assignStageAgent(stage *Stage, key, value, at string) error {
	switch key {
	case "agent":
		stage.Agent = unquote(value)
	case "brief":
		stage.Brief = unquote(value)
	case "skills":
		skills, err := parseStrings(value, at)
		if err != nil {
			return err
		}
		stage.Skills = skills
	case "tools_deny":
		denied, err := parseCapabilities(value, at)
		if err != nil {
			return err
		}
		stage.ToolsDeny = denied
	}
	return nil
}

// assignLoop places one setting inside a `[loop]` block.
func assignLoop(loop *LoopSpec, key, value, at string) error {
	switch key {
	case "converges_on":
		artifacts, err := parseArtifacts(value, at)
		if err != nil {
			return err
		}
		loop.ConvergesOn = artifacts
	case "invalidates":
		artifacts, err := parseArtifacts(value, at)
		if err != nil {
			return err
		}
		loop.Invalidates = artifacts
	case "max_rounds", "no_progress", "oscillation":
		return assignLoopLimit(&loop.Limits, key, value, at)
	default:
		return fmt.Errorf("%s: unknown key %q in [loop] "+
			"(expected converges_on, invalidates, max_rounds, no_progress, oscillation)", at, key)
	}
	return nil
}

// assignLoopLimit reads one ceiling, refusing a value that cannot bound anything.
//
// Zero is refused rather than taken as "no limit": a ceiling of zero is either a
// loop that stops before its first round or one that never stops, depending on
// which comparison reads it, and neither is what somebody typing it meant.
func assignLoopLimit(limits *LoopLimits, key, value, at string) error {
	rounds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("%s: %s has to be a number, got %q", at, key, value)
	}
	if rounds < 1 {
		return fmt.Errorf("%s: %s has to be at least 1, got %d", at, key, rounds)
	}

	switch key {
	case "max_rounds":
		limits.MaxRounds = rounds
	case "no_progress":
		limits.NoProgress = rounds
	case "oscillation":
		limits.Oscillation = rounds
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
	case "autonomy_floor":
		level, err := parseAutonomyFloor(value, at)
		if err != nil {
			return err
		}
		gate.AutonomyFloor = level
	case "judge", "judge_by_reading":
		return assignGateCriteria(gate, key, value, at)
	default:
		return fmt.Errorf("%s: unknown key %q in [gate]", at, key)
	}
	return nil
}

// assignGateCriteria puts one of the gate's two criterion lists into the spec.
//
// Split from the switch above because the two parse identically and differ only
// in where they land; keeping them inline made one function decide six things.
func assignGateCriteria(gate *GateSpec, key, value, at string) error {
	criteria, err := parseStrings(value, at)
	if err != nil {
		return err
	}
	if key == "judge" {
		gate.Judge = criteria
		return nil
	}
	gate.ReadableJudge = criteria
	return nil
}

// parseAutonomyFloor reads the lowest autonomy that absorbs a gate, refusing
// anything outside 1–10.
//
// Zero is refused rather than accepted as "undeclared": writing it is a person
// asking for a gate that every knob setting absorbs, including the one that is
// supposed to judge nothing. Leaving the key out is how you say nothing, and that
// resolves to DefaultAutonomyFloor — the opposite end.
func parseAutonomyFloor(value, at string) (int, error) {
	level, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s: autonomy_floor has to be a number 1-10, got %q", at, value)
	}
	if level < 1 || level > DefaultAutonomyFloor {
		return 0, fmt.Errorf("%s: autonomy_floor has to be 1-10, got %d", at, level)
	}
	return level, nil
}

// parseCapabilities reads a tools_deny list, refusing a name Luna does not know.
//
// A typo here fails open — the stage would run with the tool it was supposed to
// lose — so an unknown name is an error rather than a skipped entry.
func parseCapabilities(value, at string) ([]Capability, error) {
	names, err := parseStrings(value, at)
	if err != nil {
		return nil, err
	}
	denied := make([]Capability, 0, len(names))
	for _, name := range names {
		capability, err := ParseCapability(name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", at, err)
		}
		denied = append(denied, capability)
	}
	return denied, nil
}

// parseStrings reads a list of quoted strings, for the values that are prose
// rather than identifiers.
//
// Separate from parseArtifacts because a judgement criterion is a sentence: it
// contains commas, and splitting on them the way an artifact list does would cut
// one criterion into several.
func parseStrings(value, at string) ([]string, error) {
	inner, ok := strings.CutPrefix(strings.TrimSpace(value), "[")
	if !ok {
		return nil, fmt.Errorf("%s: expected a list like [\"a\", \"b\"], got %q", at, value)
	}
	inner, ok = strings.CutSuffix(strings.TrimSpace(inner), "]")
	if !ok {
		return nil, fmt.Errorf("%s: a list that opens with [ has to close with ]", at)
	}

	var list []string
	for _, item := range splitQuoted(inner) {
		if item != "" {
			list = append(list, item)
		}
	}
	return list, nil
}

// splitQuoted pulls the quoted strings out of a list body, ignoring whatever is
// between them.
//
// It reads the quotes rather than splitting on commas, which is what lets a
// criterion contain one: "no test without a docstring, and no docstring without a
// test" is a single criterion, and a comma split would make it two.
func splitQuoted(inner string) []string {
	var (
		items   []string
		current strings.Builder
		open    bool
	)

	for _, r := range inner {
		switch {
		case r == '"':
			if open {
				items = append(items, current.String())
				current.Reset()
			}
			open = !open
		case open:
			current.WriteRune(r)
		}
	}
	return items
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
	case "path":
		spec.path = unquote(value)
	case "handover":
		spec.handover = unquote(value)
	default:
		return fmt.Errorf("%s: unknown key %q in a verify block", at, key)
	}
	return nil
}

// finish assembles the stage and checks what only the whole file can answer.
func finish(stage Stage, blocks stageBlocks, verify map[Artifact]Verifier, where string) (Stage, error) {
	if stage.ID == "" {
		return Stage{}, fmt.Errorf("%s: the stage declares no id", where)
	}
	if blocks.gate.declared() {
		gate := blocks.gate
		stage.Gate = &gate
	}
	if blocks.review.SendsBackTo != "" || len(blocks.review.Invalidates) > 0 {
		review := blocks.review
		stage.Review = &review
	}
	// What it converges on rather than the limits: a loop declaring only ceilings
	// is one whose exit nothing proves, and that is the shape this refuses to
	// read as a loop at all.
	if len(blocks.loop.ConvergesOn) > 0 {
		loop := blocks.loop
		stage.Loop = &loop
	}
	if len(verify) > 0 {
		stage.Verifiers = verify
	}

	// Every artifact the flow consumes declares how it is proven. `Existence` is
	// still available and is now a choice someone wrote down, which is what an
	// artifact declaring its own verification asks for and what a default nobody
	// noticed could never be.
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

// readLine returns the next logical line and how many extra physical ones it
// swallowed.
//
// The two differ only for a list that spans lines, which is why this exists
// rather than being inlined: `judge` holds sentences a person writes, and forcing
// those onto one line would make the stage file unreadable exactly where it most
// needs reading.
func readLine(lines []string, number int, where string) (line string, consumed int, err error) {
	line = strings.TrimSpace(stripComment(lines[number]))
	if line == "" {
		return "", 0, nil
	}

	joined, consumed, err := joinList(lines, number, where)
	if err != nil {
		return "", 0, err
	}
	if consumed > 0 {
		return joined, consumed, nil
	}
	return line, 0, nil
}

// joinList gathers a list that opens on one line and closes on a later one,
// returning the single line it becomes and how many extra lines it consumed.
//
// Zero consumed means this line is not an unclosed list and the caller carries
// on unchanged — the common case, and the one that keeps every existing stage
// file parsing exactly as before.
//
// An unclosed list that reaches the end of the file is an error rather than a
// silently truncated one: a `judge` block missing its ] would otherwise load with
// however many criteria happened to precede the mistake, which is the half-loaded
// flow ParseStage refuses everywhere else.
func joinList(lines []string, start int, where string) (line string, consumed int, err error) {
	first := strings.TrimSpace(stripComment(lines[start]))

	_, value, found := strings.Cut(first, "=")
	if !found {
		return "", 0, nil
	}
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "[") || strings.Contains(value, "]") {
		return "", 0, nil
	}

	var b strings.Builder
	b.WriteString(first)
	for i := start + 1; i < len(lines); i++ {
		next := strings.TrimSpace(stripComment(lines[i]))
		b.WriteString(" ")
		b.WriteString(next)

		if strings.Contains(next, "]") {
			return b.String(), i - start, nil
		}
	}

	return "", 0, fmt.Errorf("%s:%d: a list that opens with [ has to close with ]",
		where, start+1)
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
