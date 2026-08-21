package cli

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestTheStockRolesAreTheRolesTheEngineShipped is the acceptance criterion for
// moving the roles out of Go.
//
// The Go map is gone, so the comparison is against what it produced, written
// here. That is weaker than the flow's fingerprint check and it is the honest
// shape available: a role is configuration, not history, so nothing records what
// it was.
func TestTheStockRolesAreTheRolesTheEngineShipped(t *testing.T) {
	roles := ShippedRoles()

	// The three the shipped flow names. A flow whose role does not resolve stops
	// loudly, so a missing file here is a stage that cannot run. (`setup` names
	// none: a worktree is git, and there is no judgement in making one.)
	for _, name := range []fsm.RoleName{"maker", "critic", "investigator"} {
		role, ok := roles[name]
		if !ok {
			t.Errorf("%s has no definition — the stage that names it cannot run", name)
			continue
		}
		if role.Agent != "claude" {
			t.Errorf("%s runs on %q, want claude", name, role.Agent)
		}
		if role.Brief == "" {
			t.Errorf("%s has no brief", name)
		}
	}

	if len(roles) != 3 {
		t.Errorf("got %d roles, want the 3 the flow names", len(roles))
	}
}

// TestTheReviewRoleStillCannotWrite is the write/review separation surviving the move.
//
// The denial used to be a Go value beside the brief; it is now a line in a file.
// A file that lost it would leave the reviewer able to edit the work it judges,
// and the brief saying otherwise is exactly the violation the invariant names.
//
// One role carries it now rather than four: `critic` is what `verify` and
// `review` both name, so the denial it holds is the only one standing between a
// review and the work it judges.
func TestTheReviewRoleStillCannotWrite(t *testing.T) {
	roles := ShippedRoles()

	if role := roles["critic"]; !role.DeniesWriting() {
		t.Errorf("critic can write: tools_deny = %v", role.ToolsDeny)
	}

	// And the ones that work still can.
	for _, name := range []fsm.RoleName{"maker", "investigator"} {
		if roles[name].Gated() {
			t.Errorf("%s was denied a tool it needs: %v", name, roles[name].ToolsDeny)
		}
	}
}

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

// TestARoleFileIsNamedByItsFile. The name comes from the filename so it cannot
// disagree with itself — which is what a `[role.reviewer]` header inside
// `scout.toml` would do.
func TestARoleFileIsNamedByItsFile(t *testing.T) {
	files := fstest.MapFS{
		"roles/reviewer.toml": &fstest.MapFile{Data: []byte(`
agent      = "codex"
brief      = "you review"
tools_deny = ["Edit", "Write"]
`)},
	}

	roles, err := LoadRoles(files, "roles")
	if err != nil {
		t.Fatalf("LoadRoles: %v", err)
	}
	if roles["reviewer"].Agent != "codex" {
		t.Errorf("agent = %q, want codex", roles["reviewer"].Agent)
	}
	if !roles["reviewer"].DeniesWriting() {
		t.Error("the denial did not survive the parse")
	}
}

// TestAStockFileHasNoSections. Its name is the filename, so a `[header]` is a
// file written against the config's format — refused rather than ignored, since
// ignoring it would silently drop everything under it.
func TestAStockFileHasNoSections(t *testing.T) {
	files := fstest.MapFS{
		"roles/reviewer.toml": &fstest.MapFile{Data: []byte("[role.reviewer]\nagent = \"codex\"\n")},
	}

	_, err := LoadRoles(files, "roles")
	if err == nil {
		t.Fatal("a sectioned stock file was accepted")
	}
	if !strings.Contains(err.Error(), "config.toml") {
		t.Errorf("the error does not say where sections belong: %v", err)
	}
}

// TestAnEmptyStockIsRefused. A flow names roles and every task carries a
// profile, so neither directory may come back empty.
func TestAnEmptyStockIsRefused(t *testing.T) {
	empty := fstest.MapFS{"roles/README.md": &fstest.MapFile{Data: []byte("nothing")}}

	if _, err := LoadRoles(empty, "roles"); err == nil {
		t.Error("a directory with no role files produced a role set")
	}
	if _, err := LoadRoles(empty, "nowhere"); err == nil {
		t.Error("a directory that does not exist produced a role set")
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
		"unknown key":       "agent = \"claude\"\nnonsense = \"x\"\n",
		"unknown capability": "agent = \"claude\"\n" +
			"tools_deny = [\"Telepathy\"]\n",
	} {
		t.Run(name, func(t *testing.T) {
			files := fstest.MapFS{"roles/x.toml": &fstest.MapFile{Data: []byte(content)}}
			if _, err := LoadRoles(files, "roles"); err == nil {
				t.Error("accepted")
			}
		})
	}

	files := fstest.MapFS{"profiles/x.toml": &fstest.MapFile{Data: []byte("turn_budget = \"not a duration\"\n")}}
	if _, err := LoadProfiles(files, "profiles"); err == nil {
		t.Error("a profile whose budget cannot be read was accepted")
	}
}

// TestAProjectStillOverridesTheStock. The stock is the default; a project's
// config.toml is what changes it, and naming one role must not delete the rest.
func TestAProjectStillOverridesTheStock(t *testing.T) {
	cfg, err := parseConfig(`
[role.critic]
agent = "codex"
`, "config.toml")
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}

	if cfg.Roles["critic"].Agent != "codex" {
		t.Errorf("the override did not take: %+v", cfg.Roles["critic"])
	}
	if len(cfg.Roles) != 3 {
		t.Errorf("got %d roles, want the other 2 still there", len(cfg.Roles))
	}
	if cfg.Roles["maker"].Brief == "" {
		t.Error("naming one role deleted another's brief")
	}
}

// TestTheCriticIsTaughtTheVocabularyLunaReads is the transport that was missing
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
func TestTheCriticIsTaughtTheVocabularyLunaReads(t *testing.T) {
	critic, ok := ShippedRoles()["critic"]
	if !ok {
		t.Fatal("the shipped stock has no critic role")
	}

	for _, severity := range fsm.KnownSeverities() {
		if !strings.Contains(critic.Brief, "["+string(severity)+"]") {
			t.Errorf("the critic is never told about [%s], so a finding it tags that way "+
				"is one Luna cannot read", severity)
		}
	}

	// And the rule for the one that sends work back, which is the whole reason
	// the vocabulary is narrow: an agent told only that BLOCKING exists reaches
	// for it on any defect it considers serious, and every inherited one reopens
	// the work.
	if !strings.Contains(critic.Brief, "introduced") {
		t.Errorf("the critic is not told what may block, only that blocking exists:\n%s", critic.Brief)
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
