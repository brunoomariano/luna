package contract

import (
	"fmt"
	"strconv"
	"strings"
)

// Parse reads a contract from TOML.
//
// It refuses rather than guesses, in every direction: an unknown key, an unknown
// scope, an artifact with no declared verifier. A contract that half-loads is
// worse than one that will not load, because the half that is missing is
// invisible until the check passes for the wrong reason.
//
// `where` names the source in errors. It is usually "<stdin>", since a contract
// arrives on a pipe.
func Parse(content, where string) (Contract, error) {
	p := &parser{
		where:    where,
		contract: Contract{Produces: map[string]Verifier{}, ForHuman: map[string]Verifier{}},
		specs:    map[string]*verifierSpec{},
	}
	if err := p.run(content); err != nil {
		return Contract{}, err
	}
	return p.finish()
}

// parser accumulates a contract as the lines arrive.
//
// The verifier blocks are collected first and built at the end because a block's
// meaning depends on which keys it carries, and TOML delivers them one at a time.
type parser struct {
	where    string
	section  string
	contract Contract
	specs    map[string]*verifierSpec
	loop     *Loop
}

// verifierSpec is a `[verify.<artifact>]` block before it becomes a Verifier.
type verifierSpec struct {
	run   string
	scope string
	kind  string
	path  string
	at    string
}

func (p *parser) run(content string) error {
	lines := strings.Split(content, "\n")
	for number := 0; number < len(lines); number++ {
		line, consumed, err := readLine(lines, number, p.where)
		if err != nil {
			return err
		}
		at := fmt.Sprintf("%s:%d", p.where, number+1)
		number += consumed
		if line == "" {
			continue
		}

		if header, ok := sectionName(line); ok {
			if err := p.enter(header, at); err != nil {
				return err
			}
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			return fmt.Errorf("%s: expected key = value, got %q", at, line)
		}
		if err := p.assign(strings.TrimSpace(key), strings.TrimSpace(value), at); err != nil {
			return err
		}
	}
	return nil
}

// enter opens a section, refusing one it does not know.
func (p *parser) enter(header, at string) error {
	p.section = header
	switch header {
	case "loop":
		p.loop = &Loop{}
		return nil
	case "":
		return fmt.Errorf("%s: a section needs a name", at)
	}
	artifact, ok := strings.CutPrefix(header, "verify.")
	if !ok || artifact == "" {
		return fmt.Errorf("%s: unknown section [%s] — a contract has [loop] and [verify.<artifact>]", at, header)
	}
	p.specs[artifact] = &verifierSpec{at: at}
	return nil
}

// assign routes one key to whichever section is open.
func (p *parser) assign(key, value, at string) error {
	switch p.section {
	case "":
		return p.assignTop(key, value, at)
	case "loop":
		return p.assignLoop(key, value, at)
	}
	return p.assignVerifier(key, value, at)
}

func (p *parser) assignTop(key, value, at string) error {
	switch key {
	case "phase":
		p.contract.Phase = unquote(value)
	case "requires":
		list, err := parseStrings(value, at)
		if err != nil {
			return err
		}
		p.contract.Requires = list
	case "produces":
		return p.declare(value, at, false)
	case "produces_for_human":
		return p.declare(value, at, true)
	default:
		return fmt.Errorf("%s: unknown key %q — a contract has phase, requires, produces, produces_for_human", at, key)
	}
	return nil
}

// declare records the artifacts a side of the contract owes, with no verifier
// yet. The verifier arrives from its own `[verify.<artifact>]` block, and an
// artifact that never gets one is refused by Lint.
func (p *parser) declare(value, at string, forHuman bool) error {
	list, err := parseStrings(value, at)
	if err != nil {
		return err
	}
	for _, name := range list {
		if forHuman {
			p.contract.ForHuman[name] = nil
			continue
		}
		p.contract.Produces[name] = nil
	}
	return nil
}

func (p *parser) assignLoop(key, value, at string) error {
	switch key {
	case "converges_on":
		list, err := parseStrings(value, at)
		if err != nil {
			return err
		}
		p.loop.ConvergesOn = list
		return nil
	case "max_rounds":
		return assignInt(&p.loop.MaxRounds, value, at, key)
	case "no_progress":
		return assignInt(&p.loop.NoProgress, value, at, key)
	case "oscillation":
		return assignInt(&p.loop.Oscillation, value, at, key)
	}
	return fmt.Errorf("%s: unknown key %q in [loop] — it has converges_on, max_rounds, no_progress, oscillation", at, key)
}

func (p *parser) assignVerifier(key, value, at string) error {
	artifact := strings.TrimPrefix(p.section, "verify.")
	spec, open := p.specs[artifact]
	if !open {
		return fmt.Errorf("%s: %q outside any section", at, key)
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
	default:
		return fmt.Errorf("%s: unknown key %q in [%s] — it has run, scope, kind, path", at, key, p.section)
	}
	return nil
}

func assignInt(target *int, value, at, key string) error {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("%s: %s has to be a whole number, got %q", at, key, value)
	}
	*target = n
	return nil
}

// finish builds every collected block and attaches it to the artifact it names.
//
// A `[verify.x]` block for an artifact the contract does not owe is an error
// rather than dead configuration: it is how a rename leaves a check behind that
// looks like it is running and is not.
func (p *parser) finish() (Contract, error) {
	p.contract.Loop = p.loop

	for artifact, spec := range p.specs {
		verifier, err := spec.build(artifact)
		if err != nil {
			return Contract{}, err
		}
		switch {
		case contains(p.contract.Produces, artifact):
			p.contract.Produces[artifact] = verifier
		case contains(p.contract.ForHuman, artifact):
			p.contract.ForHuman[artifact] = verifier
		default:
			return Contract{}, fmt.Errorf(
				"%s: [verify.%s] declares a check for an artifact this contract does not produce",
				spec.at, artifact)
		}
	}
	return p.contract, nil
}

func contains(set map[string]Verifier, name string) bool {
	_, ok := set[name]
	return ok
}

// build turns the collected keys into a verifier, refusing anything ambiguous.
//
// `run` and `kind = "existence"` together is the ambiguity that matters: one says
// a command proves it and the other says nothing does, and picking either would
// make a contract mean something its author did not write.
func (s *verifierSpec) build(artifact string) (Verifier, error) {
	hasRun := strings.TrimSpace(s.run) != ""

	switch {
	case hasRun && s.kind != "":
		return nil, fmt.Errorf("%s: [verify.%s] declares both `run` and `kind` — a command proves it, or nothing does",
			s.at, artifact)
	case hasRun && s.path != "":
		return nil, fmt.Errorf("%s: [verify.%s] declares both `run` and `path` — `path` belongs to an existence check",
			s.at, artifact)
	case hasRun:
		return s.buildCommand(artifact)
	case s.scope != "":
		return nil, fmt.Errorf("%s: [verify.%s] declares a scope with nothing to run — an existence check proves existence and says so",
			s.at, artifact)
	case s.kind != "" && s.kind != "existence":
		return nil, fmt.Errorf("%s: [verify.%s] unknown kind %q — the only kind is \"existence\"; anything else is a command with `run`",
			s.at, artifact, s.kind)
	}
	return Existence{Path: s.path}, nil
}

func (s *verifierSpec) buildCommand(artifact string) (Verifier, error) {
	command := Command{Run: s.run}
	if s.scope == "" {
		return command, nil
	}
	scope := Scope(s.scope)
	if !scope.Valid() {
		return nil, fmt.Errorf("%s: [verify.%s] scope %q is not one of full, targeted, existence, human",
			s.at, artifact, s.scope)
	}
	command.Scope = scope
	return command, nil
}
