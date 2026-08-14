package registry

import (
	"encoding/json"
	"strings"
	"testing"
)

// taskWithMetadata decodes a task the way the registry does, from the JSON `bd
// show --json` actually returns.
//
// Going through the decoder rather than building the struct by hand is the point
// of these tests: what is being covered is Luna's ability to read a real task,
// and a hand-built Metadata map would skip the step where that goes wrong.
func taskWithMetadata(t *testing.T, metadata string) Task {
	t.Helper()

	var tasks []Task
	body := `[{"id":"LUNA-1","title":"a task","status":"open","metadata":` + metadata + `}]`
	if err := json.Unmarshal([]byte(body), &tasks); err != nil {
		t.Fatalf("decoding a task with metadata %s: %v", metadata, err)
	}
	return tasks[0]
}

// TestChecksAreReadPerGate covers the shape measured against bd 1.2.1.
func TestChecksAreReadPerGate(t *testing.T) {
	task := taskWithMetadata(t, `{"luna_gates":{
		"approve-plan":{"checks":["make fmt","make typecheck"]},
		"approve-spec":{"checks":["make ci"]}
	}}`)

	checks, declared, err := task.ChecksFor("approve-plan")
	if err != nil {
		t.Fatalf("reading approve-plan: %v", err)
	}
	if !declared {
		t.Fatal("approve-plan declared checks and they were not found")
	}
	if len(checks) != 2 || checks[0] != "make fmt" || checks[1] != "make typecheck" {
		t.Errorf("approve-plan checks: got %q", checks)
	}

	// Keyed per gate: the other gate's checks must not leak into this one.
	spec, _, err := task.ChecksFor("approve-spec")
	if err != nil {
		t.Fatalf("reading approve-spec: %v", err)
	}
	if len(spec) != 1 || spec[0] != "make ci" {
		t.Errorf("approve-spec checks: got %q", spec)
	}
}

// TestAGateNobodyDeclaredIsNotAnError is the normal case — it is every task that
// exists today, and it has to stay silent.
func TestAGateNobodyDeclaredIsNotAnError(t *testing.T) {
	cases := map[string]string{
		"no metadata at all":     `{}`,
		"metadata without luna":  `{"someone_else":{"their":"business"}}`,
		"luna_gates but no gate": `{"luna_gates":{"approve-spec":{"checks":["make ci"]}}}`,
	}

	for name, metadata := range cases {
		t.Run(name, func(t *testing.T) {
			checks, declared, err := taskWithMetadata(t, metadata).ChecksFor("approve-plan")
			if err != nil {
				t.Fatalf("undeclared gate reported an error: %v", err)
			}
			if declared {
				t.Error("a gate nobody declared reported as declared")
			}
			if checks != nil {
				t.Errorf("a gate nobody declared produced checks: %q", checks)
			}
		})
	}
}

// TestDeclaredAndEmptyIsNotTheSameAsUndeclared covers the distinction the two
// return values exist for.
//
// "This gate has no mechanical answer" is a statement a person may want to make,
// and it sends the gate somewhere different from "this task says nothing about
// this gate". A nil slice alone could not tell them apart.
func TestDeclaredAndEmptyIsNotTheSameAsUndeclared(t *testing.T) {
	checks, declared, err := taskWithMetadata(t,
		`{"luna_gates":{"approve-plan":{"checks":[]}}}`).ChecksFor("approve-plan")
	if err != nil {
		t.Fatalf("reading an empty declaration: %v", err)
	}

	if !declared {
		t.Error("an explicit empty declaration read as undeclared")
	}
	if len(checks) != 0 {
		t.Errorf("an empty declaration produced checks: %q", checks)
	}
}

// TestAnotherToolsMetadataDoesNotCostLunaTheTask is a regression test for a bug
// found by probing the real binary.
//
// Metadata is a bag shared with whatever else writes to the task, and beads puts
// no constraint on the shape of a key that is not Luna's. Typing the field as
// `map[string]map[string]GateChecks` decoded the happy path perfectly and then
// failed the *entire task* the moment another tool stored a scalar — Luna losing
// a task over data that was never its own.
func TestAnotherToolsMetadataDoesNotCostLunaTheTask(t *testing.T) {
	foreign := map[string]string{
		"a scalar string": `{"theirs":"a string","luna_gates":{"approve-plan":{"checks":["make ci"]}}}`,
		"a number":        `{"theirs":42,"luna_gates":{"approve-plan":{"checks":["make ci"]}}}`,
		"an array":        `{"theirs":[1,2,3],"luna_gates":{"approve-plan":{"checks":["make ci"]}}}`,
		"a deeper object": `{"theirs":{"a":{"b":"c"}},"luna_gates":{"approve-plan":{"checks":["make ci"]}}}`,
		"null":            `{"theirs":null,"luna_gates":{"approve-plan":{"checks":["make ci"]}}}`,
	}

	for name, metadata := range foreign {
		t.Run(name, func(t *testing.T) {
			// taskWithMetadata fatals on a decode failure, which is the bug itself.
			checks, declared, err := taskWithMetadata(t, metadata).ChecksFor("approve-plan")
			if err != nil {
				t.Fatalf("another tool's metadata broke the read: %v", err)
			}
			if !declared || len(checks) != 1 || checks[0] != "make ci" {
				t.Errorf("Luna's own declaration did not survive: declared=%v checks=%q",
					declared, checks)
			}
		})
	}
}

// TestAMalformedDeclarationIsLoud covers the one case that must not be silent.
//
// `luna_gates` is the person's own statement about their own task. A
// declaration that will not parse is their mistake, and answering the gate by
// asking them — which is what "no checks" would do — hides the very thing they
// were trying to fix.
func TestAMalformedDeclarationIsLoud(t *testing.T) {
	malformed := map[string]string{
		"a scalar where the gates go":  `{"luna_gates":"make ci"}`,
		"checks as a string":           `{"luna_gates":{"approve-plan":{"checks":"make ci"}}}`,
		"a gate that is not an object": `{"luna_gates":{"approve-plan":7}}`,
	}

	for name, metadata := range malformed {
		t.Run(name, func(t *testing.T) {
			_, declared, err := taskWithMetadata(t, metadata).ChecksFor("approve-plan")
			if err == nil {
				t.Fatal("a malformed luna_gates was read without complaint")
			}
			if declared {
				t.Error("a malformed declaration reported as declared")
			}
			// The error has to name the task and the key, per AGENTS.md: the person
			// reading it is looking for which task they mistyped.
			if !strings.Contains(err.Error(), "LUNA-1") ||
				!strings.Contains(err.Error(), lunaGates) {
				t.Errorf("the error names neither the task nor the key: %v", err)
			}
		})
	}
}
