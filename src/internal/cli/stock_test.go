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

	// The pack the shipped flows name, plus the one a solo run collapses onto. A
	// flow whose role does not resolve stops loudly, so a missing file here is a
	// stage that cannot run. (`setup` and `pipeline` name none: a worktree is git
	// and `ci_green` is a command's verdict, and there is no judgement in either.)
	for _, name := range []string{"lead", "planner", "investigator", "coder", "cleaner", "auditor"} {
		role, ok := roles[fsm.RoleName(name)]
		if !ok {
			t.Errorf("%s has no definition — every stage that names it cannot run", name)
			continue
		}
		if role.Agent != "claude" {
			t.Errorf("%s runs on %q, want claude", name, role.Agent)
		}
		if role.Brief == "" {
			t.Errorf("%s has no brief", name)
		}
	}

	// The auditor is the one that has to deny, and it is what a pack buys back:
	// with a single agent doing everything, `tools_deny` cannot separate writing
	// from judging, so the audit was not independent and did not claim to be. A
	// different agent makes the denial mean something again.
	if len(roles["auditor"].ToolsDeny) == 0 {
		t.Error("the auditor denies nothing, so it can edit what it is judging")
	}

	// Six, and the number is the point rather than an accident: five specialisms
	// plus the solo role. A seventh file appearing here is a design change that
	// has to be argued rather than typed.
	if len(roles) != 6 {
		t.Errorf("got %d roles, want the 6 the flows name", len(roles))
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
	if len(roles["reviewer"].ToolsDeny) != 2 {
		t.Errorf("the denial did not survive the parse: %v", roles["reviewer"].ToolsDeny)
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

// TestAProjectStillOverridesTheRole. The stock is the default; a project's
// config.toml is what changes it, and naming one field must not clear the rest.
//
// This is the last override a project has, now that flows come only from the
// binary. It is narrower than it looks: it changes which harness runs a stage or
// what it is told, and it cannot change what the stage owes or how that is proven.
func TestAProjectStillOverridesTheRole(t *testing.T) {
	cfg, err := parseConfig(`
[role.lead]
agent = "codex"

[role.scribe]
agent = "claude"
brief = "You write things down."
`, "config.toml")
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}

	if cfg.Roles["lead"].Agent != "codex" {
		t.Errorf("the override did not take: %+v", cfg.Roles["lead"])
	}
	// The other role is untouched. A config that names one role must not clear the
	// rest: the flow names roles a config never mentions, and deleting them would
	// leave a stage with nothing to run.
	if cfg.Roles["scribe"].Brief == "" {
		t.Error("naming one role cleared another's brief")
	}
	// The stock's roles plus the one the project added: naming one role must not
	// clear the rest.
	if len(cfg.Roles) != len(ShippedRoles())+1 {
		t.Errorf("got %d roles, want the stock's %d plus the project's one",
			len(cfg.Roles), len(ShippedRoles()))
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
	critic, ok := ShippedRoles()["lead"]
	if !ok {
		t.Fatal("the shipped stock has no lead role")
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

// TestTheLeadIsToldAContractAdmitsNoRecommendation covers the rule the gate
// enforces and nothing stated.
//
// Two contracts in a row were rejected for the same criterion — "with no
// suggestions" — and neither was a lapse in writing. Both were otherwise
// properly imperative, and both put the offending sentence in a section the
// second one titled "Note for the maker". That is an agent being helpful in a
// document with no room for help.
func TestTheLeadIsToldAContractAdmitsNoRecommendation(t *testing.T) {
	maker, ok := ShippedRoles()["lead"]
	if !ok {
		t.Fatal("the shipped stock has no lead role")
	}

	for _, want := range []string{"contract", "recommendation", "obligation"} {
		if !strings.Contains(maker.Brief, want) {
			t.Errorf("the maker is not told what a contract admits (%q):\n%s", want, maker.Brief)
		}
	}
}

// TestTheAuditStageDoesNotClaimIndependence is what replaced two tests whose
// property died with the roles.
//
// They asserted that the judging role could not write — `tools_deny` on a second
// role, refused a session with the first. With one agent, neither is available:
// the agent that must edit cannot be denied Edit, and there is no other session
// to keep it out of. So the claim goes, and the stage is named for what it is.
//
// What survives is `context = "fresh"`. It is the one half of independence a
// single agent can still have — the same model, re-reading its own work with no
// memory of having written it — and without it the stage reads its own reasoning
// back and confirms it, which is the failure a review exists to prevent.
func TestTheAuditStageDoesNotClaimIndependence(t *testing.T) {
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
		if stage.ID == "review" {
			t.Errorf("a stage still calls itself %q, which claims an independence "+
				"one agent cannot have", stage.ID)
		}
		if stage.Context != fsm.ContextFresh {
			t.Errorf("stage %q judges delivered work with context %q — it would read "+
				"back the session that produced it", stage.ID, stage.Context)
		}
	}
}
