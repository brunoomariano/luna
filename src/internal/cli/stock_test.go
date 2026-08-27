package cli

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestTheStockProfilesMatchTheShippedPolicy ties the files to the function that
// replays old logs.
//
// `fsm.ShippedPolicy` stays in Go on purpose: it is what an event recorded before
// gate decisions existed replays as, so it is frozen history rather
// than configuration. The files are the *defaults a project inherits*, and the
// two must agree — a profile whose file said something else would make a replay
// and a fresh run disagree about the same name.
func TestTheStockProfilesMatchTheShippedPolicy(t *testing.T) {
	profiles := ShippedProfiles()

	for _, name := range fsm.ShippedProfiles() {
		if !profiles[name] {
			t.Errorf("%s has no file", name)
		}
	}
}

// TestAStockFileHasNoSections. Its name is the filename, so a `[header]` is a
// file written against the config's format — refused rather than ignored, since
// ignoring it would silently drop everything under it.
func TestAStockFileHasNoSections(t *testing.T) {
	files := fstest.MapFS{
		"profiles/nightly.toml": &fstest.MapFile{Data: []byte("[profile.nightly]\nturn_budget = \"1h\"\n")},
	}

	_, err := LoadProfiles(files, "profiles")
	if err == nil {
		t.Fatal("a sectioned stock file was accepted")
	}
	if !strings.Contains(err.Error(), "config.toml") {
		t.Errorf("the error does not say where sections belong: %v", err)
	}
}

// TestAnEmptyStockIsRefused. Every task carries a profile, so the directory may
// not come back empty.
func TestAnEmptyStockIsRefused(t *testing.T) {
	if _, err := LoadProfiles(fstest.MapFS{}, "nowhere"); err == nil {
		t.Error("a directory that does not exist produced a profile set")
	}

	noProfiles := fstest.MapFS{"profiles/README.md": &fstest.MapFile{Data: []byte("nothing")}}
	if _, err := LoadProfiles(noProfiles, "profiles"); err == nil {
		t.Error("a directory with no profile files produced a profile set")
	}
	if _, err := LoadProfiles(noProfiles, "nowhere"); err == nil {
		t.Error("a directory that does not exist produced a profile set")
	}
}

// TestAProfileFileWithNothingInItIsStillAProfile. A profile decides nothing now
// — the stage declares whether a gate waits and the knob decides who
// answers — so the shipped files carry only comments. The name still has to
// exist, because `task new --profile` validates against this set and a task's
// log carries the name as history.
func TestAProfileFileWithNothingInItIsStillAProfile(t *testing.T) {
	files := fstest.MapFS{
		"profiles/yolo.toml": &fstest.MapFile{Data: []byte("# nothing but a name\n")},
	}

	profiles, err := LoadProfiles(files, "profiles")
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}

	if _, ok := profiles["yolo"]; !ok {
		t.Error("a profile the project defined did not appear at all")
	}
}

// TestASettingInAStockProfileIsRefused. A profile file declares a name and
// nothing else since profiles stopped governing gates; loading a setting silently would leave a project
// believing a per-profile watchdog still applies.
func TestASettingInAStockProfileIsRefused(t *testing.T) {
	files := fstest.MapFS{
		"profiles/yolo.toml": &fstest.MapFile{Data: []byte("turn_budget = \"30m\"\n")},
	}

	_, err := LoadProfiles(files, "profiles")

	if err == nil {
		t.Fatal("a budget inside a profile must be reported")
	}
	if !strings.Contains(err.Error(), "only its name") {
		t.Errorf("the error should say a profile holds no settings, got %v", err)
	}
}

// TestABrokenStockFileIsRefused covers the ways a file can be wrong. Each one is
// a refusal, because a role that half-loaded would run an agent with half a
// definition.
func TestABrokenStockFileIsRefused(t *testing.T) {
	for name, content := range map[string]string{
		"not a key = value": "this is not toml\n",
		"unknown key":       "nonsense = \"x\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			files := fstest.MapFS{"profiles/x.toml": &fstest.MapFile{Data: []byte(content)}}
			if _, err := LoadProfiles(files, "profiles"); err == nil {
				t.Error("accepted")
			}
		})
	}

	files := fstest.MapFS{"profiles/x.toml": &fstest.MapFile{Data: []byte("turn_budget = \"not a duration\"\n")}}
	if _, err := LoadProfiles(files, "profiles"); err == nil {
		t.Error("a profile whose budget cannot be read was accepted")
	}
}

// TestTheLeadIsTaughtTheVocabularyLunaReads is the transport that was missing
// between two working ends.
//
// `fsm.ReadReport` looks for `[BLOCKING]`, `[SHOULD-FIX]`, `[NIT]` and
// `[UNCERTAIN]`; a `[BLOCKING]` is what sends work back to build. The parser was
// written, the stage declared `sends_back_to`, and nothing ever told the agent
// the tags existed.
//
// Measured on TALLY-7: the critic found four real defects, wrote them under a
// heading called "Findings" in prose, and the parser read nothing. The flow
// carried on and four verified defects moved nothing.
func TestTheLeadIsTaughtTheVocabularyLunaReads(t *testing.T) {
	critic := fsm.SoloBrief

	for _, severity := range fsm.KnownSeverities() {
		if !strings.Contains(critic, "["+string(severity)+"]") {
			t.Errorf("the critic is never told about [%s], so a finding it tags that way "+
				"is one Luna cannot read", severity)
		}
	}

	// And the rule for the one that sends work back, which is the whole reason
	// the vocabulary is narrow: an agent told only that BLOCKING exists reaches
	// for it on any defect it considers serious, and every inherited one reopens
	// the work.
	if !strings.Contains(critic, "introduced") {
		t.Errorf("the critic is not told what may block, only that blocking exists:\n%s", critic)
	}
}

// TestThePlanGateAsksOnlyWhatItShows keeps a gate from asking a question its own
// evidence cannot answer.
//
// The gate carries the contract and nothing else. Its third criterion read "none
// is impossible to satisfy", which is a question about code: whether an
// obligation can be met depends on the script being changed, and the gate never
// shows it. Measured on TALLY-8, where the lead answered honestly — "depends on
// what today's tally.sh actually contains, which I cannot read" — marked the
// criterion unsupported, and the gate went to a person at knob 9.
//
// That is the same lesson the stage file already records for `scenarios`: asking
// about what is not shown teaches whoever answers to guess. Whether the delivery
// satisfies the contract belongs to `verify`, which requires the contract and
// has the code.
func TestThePlanGateAsksOnlyWhatItShows(t *testing.T) {
	gate := fsm.GateSpecIn(fsm.DefaultFlow(), "plan")
	if gate == nil {
		t.Fatal("the shipped plan stage declares no gate")
	}

	for _, criterion := range gate.Judge {
		if strings.Contains(criterion, "impossible to satisfy") {
			t.Errorf("the gate asks whether an obligation is satisfiable, which needs the "+
				"code it does not carry: %q", criterion)
		}
	}

	// And every criterion stays answerable by reading, because that is what the
	// gate hands over. One that is not exempt is one the lead must mark
	// unsupported, and the gate goes to a person at every autonomy.
	if len(gate.ReadableJudge) != len(gate.Judge) {
		t.Errorf("the gate judges %d criteria and exempts %d — the difference can only "+
			"be answered by a check this gate has nothing to run",
			len(gate.Judge), len(gate.ReadableJudge))
	}
}

// TestTheLeadIsToldAContractAdmitsNoRecommendation covers the rule the gate
// enforces and nothing stated.
//
// Two contracts in a row were rejected for the same criterion — "with no
// suggestions" — and neither was a lapse in writing. Both were otherwise
// properly imperative, and both put the offending sentence in a section the
// second one titled "Note for the maker". That is an agent being helpful in a
// document with no room for help.
func TestTheLeadIsToldAContractAdmitsNoRecommendation(t *testing.T) {
	maker := fsm.SoloBrief

	for _, want := range []string{"contract", "recommendation", "obligation"} {
		if !strings.Contains(maker, want) {
			t.Errorf("the maker is not told what a contract admits (%q):\n%s", want, maker)
		}
	}
}

// TestTheJudgingStageIsIndependent is the property the trail was reshaped to get
// back.
//
// It was lost twice and for different reasons. The roles collapsed once, leaving
// one agent that had to both write and judge — `tools_deny` cannot separate those
// when the same process does both. Then `forge` merged the building and the
// checking on purpose, so the loop could fix what it found without paying a cold
// start, which makes its own verdict a self-assessment by construction.
//
// Neither is fixable from inside. So the independent read is placed *after* the
// delivery instead of within it, and it has all three halves this time: a session
// it did not write (`fresh`), tools it does not hold (`tools_deny`), and work it
// did not do. A judging stage missing any of them reads its own reasoning back
// and agrees with it, which is the one failure a review exists to prevent.
func TestTheJudgingStageIsIndependent(t *testing.T) {
	var judging []fsm.Stage
	for _, stage := range fsm.DefaultFlow() {
		if stage.Review != nil {
			judging = append(judging, stage)
		}
	}
	if len(judging) == 0 {
		t.Fatal("no judging stage in the shipped flow — this check covers nothing")
	}

	for _, stage := range judging {
		if stage.Context != fsm.ContextFresh {
			t.Errorf("stage %q judges delivered work with context %q — it would read "+
				"back the session that produced it", stage.ID, stage.Context)
		}
		if !stage.Gated() {
			t.Errorf("stage %q judges a delivery and may still edit it", stage.ID)
		}
		if stage.Review.SendsBackTo == stage.ID {
			t.Errorf("stage %q sends work back to itself, which is not a review", stage.ID)
		}
	}
}
