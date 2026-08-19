package fsm

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestAnUnknownEnumStopsTheDecode is the failure neither journalling nor replay
// catches.
//
// Go's default is to accept any string into a `~string` type, so a value from a
// newer version lands in the field and reaches the reducer as something no switch
// matches. The branch taken is whatever the default happens to be, and the
// decision comes out wrong with nothing saying why.
func TestAnUnknownEnumStopsTheDecode(t *testing.T) {
	cases := []struct {
		what string
		into any
		json string
	}{
		{"a verdict", new(Verdict), `"probably"`},
		{"an evidence scope", new(Scope), `"mutation"`},
		{"a task kind", new(TaskKind), `"refactor"`},
		{"a gate decision", new(GateWaited), `"deferred"`},
	}

	for _, c := range cases {
		err := json.Unmarshal([]byte(c.json), c.into)
		if !errors.Is(err, ErrUnknownValue) {
			t.Errorf("%s of %s should be refused, got %v", c.what, c.json, err)
			continue
		}
		// The message has to name the value and what was expected: whoever reads
		// it is looking at a log written by software they may not have.
		if !strings.Contains(err.Error(), strings.Trim(c.json, `"`)) {
			t.Errorf("%s: the refusal should quote the value, got %q", c.what, err)
		}
	}
}

// TestEveryKnownValueStillDecodes is the other half, and the one that would break
// silently if a constant were added without updating its list.
func TestEveryKnownValueStillDecodes(t *testing.T) {
	for _, v := range KnownVerdicts() {
		var got Verdict
		if err := json.Unmarshal([]byte(`"`+string(v)+`"`), &got); err != nil || got != v {
			t.Errorf("verdict %q must decode: got %q, %v", v, got, err)
		}
	}
	for _, s := range KnownScopes() {
		var got Scope
		if err := json.Unmarshal([]byte(`"`+string(s)+`"`), &got); err != nil || got != s {
			t.Errorf("scope %q must decode: got %q, %v", s, got, err)
		}
	}
	for _, k := range KnownTaskKinds() {
		var got TaskKind
		if err := json.Unmarshal([]byte(`"`+string(k)+`"`), &got); err != nil || got != k {
			t.Errorf("kind %q must decode: got %q, %v", k, got, err)
		}
	}
	for _, d := range KnownGateDecisions() {
		var got GateWaited
		if err := json.Unmarshal([]byte(`"`+string(d)+`"`), &got); err != nil || got != d {
			t.Errorf("gate decision %q must decode: got %q, %v", d, got, err)
		}
	}
}

// TestTheAbsentGateDecisionStillDecodes covers the compatibility case.
//
// An event written before the field existed decodes to the empty decision, which
// is how replay knows to fall back to the profile. Refusing it would make every
// early log unreadable — the same reason an empty flow fingerprint matches
// anything.
func TestTheAbsentGateDecisionStillDecodes(t *testing.T) {
	var got GateWaited
	if err := json.Unmarshal([]byte(`""`), &got); err != nil {
		t.Fatalf("an event predating the field must still replay: %v", err)
	}
	if _, recorded := got.Waits(); recorded {
		t.Error("the empty decision records nothing and must fall back to the profile")
	}
}

// TestAProfileIsNotClosed guards an extension point from this change.
//
// ShippedProfiles names the three Luna comes with, and its own doc says the engine
// does not validate against it: a name it has never heard of is a profile somebody
// defined. Closing it alongside the others would have broken
// that in passing, which is why the exclusion is tested rather than assumed.
func TestAProfileIsNotClosed(t *testing.T) {
	var got Profile
	if err := json.Unmarshal([]byte(`"mine"`), &got); err != nil {
		t.Fatalf("a project may define its own profile: %v", err)
	}
	if got != "mine" {
		t.Errorf("the configured name must survive, got %q", got)
	}
}

// TestAnUnknownEnumFailsTheWholeAction is what the refusal is for.
//
// Decoding an enum is not the point — the point is that an action carrying an
// unreadable value never reaches the reducer. Before this, a Complete with a
// verdict from a newer version decoded cleanly and blocked the task claiming
// verification had failed, which sends whoever investigates to look at the test
// suite.
func TestAnUnknownEnumFailsTheWholeAction(t *testing.T) {
	payload := `{"delivered":["code"],"evidence":{"code":{"scope":"full","verdict":"probably"}}}`

	var a Complete
	err := json.Unmarshal([]byte(payload), &a)
	if !errors.Is(err, ErrUnknownValue) {
		t.Fatalf("an action carrying an unreadable value must not decode, got %v", err)
	}
}
