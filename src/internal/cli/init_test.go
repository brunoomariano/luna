package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// initHarness gives a harness a project directory to write a stock into.
func initHarness(t *testing.T) *harness {
	t.Helper()

	h := newHarness(t)
	h.env.Stock = filepath.Join(t.TempDir(), "stock")
	return h
}

// TestInitWritesTheWholeStock is what turns the replaceable flow into
// something a person can replace.
func TestInitWritesTheWholeStock(t *testing.T) {
	h := initHarness(t)

	out := h.mustRun(t, "init")

	if !strings.Contains(out, "wrote") {
		t.Errorf("nothing was reported as written:\n%s", out)
	}

	stages, err := os.ReadDir(filepath.Join(h.env.Stock, "stages"))
	if err != nil {
		t.Fatalf("reading the written stages: %v", err)
	}
	if len(stages) != len(fsm.DefaultFlow()) {
		t.Errorf("wrote %d stage files for a flow of %d stages", len(stages), len(fsm.DefaultFlow()))
	}

	// The copy is complete: a directory holding three of fourteen stages raises
	// "does this replace the default or extend it?", and either answer is one
	// somebody reads the other way.
	for _, dir := range []string{"stages", "roles", "profiles"} {
		entries, err := os.ReadDir(filepath.Join(h.env.Stock, dir))
		if err != nil || len(entries) == 0 {
			t.Errorf("%s was not written", dir)
		}
	}
}

// TestWhatInitWritesIsWhatLunaRuns closes the loop: a copied stock has to parse
// back into the flow it was copied from, or `luna init` hands someone a broken
// project.
func TestWhatInitWritesIsWhatLunaRuns(t *testing.T) {
	h := initHarness(t)
	h.mustRun(t, "init")

	files, ok := ProjectStock(h.env.Stock)
	if !ok {
		t.Fatal("what init wrote is not recognised as a stock")
	}

	flow, err := fsm.LoadFlow(files, "stages")
	if err != nil {
		t.Fatalf("the written stock does not parse: %v", err)
	}
	if got, want := fsm.Fingerprint(flow), fsm.Fingerprint(fsm.DefaultFlow()); got != want {
		t.Errorf("the copy is a different flow:\n  written: %s\n  shipped: %s", got, want)
	}
}

// TestInitRefusesToDiscardEdits. The files it writes are ones a person edits,
// and overwriting them silently would throw away exactly the customisation the
// command exists to enable.
func TestInitRefusesToDiscardEdits(t *testing.T) {
	h := initHarness(t)
	h.mustRun(t, "init")

	edited := filepath.Join(h.env.Stock, "stages", "090-verify.toml")
	before, err := os.ReadFile(edited)
	if err != nil {
		t.Fatalf("reading a written stage: %v", err)
	}
	mine := string(before) + "\n# my note\n"
	if err := os.WriteFile(edited, []byte(mine), 0o600); err != nil {
		t.Fatalf("editing it: %v", err)
	}

	err = h.run(t, "init")
	if err == nil {
		t.Fatal("a second init overwrote the project's stock")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("the refusal does not say how to override it: %v", err)
	}

	after, _ := os.ReadFile(edited)
	if string(after) != mine {
		t.Error("the edit was discarded by a command that refused")
	}
}

// TestForceReplacesTheStock is the other half: someone who means it can start
// over, and is told what that costs in the refusal above.
func TestForceReplacesTheStock(t *testing.T) {
	h := initHarness(t)
	h.mustRun(t, "init")

	edited := filepath.Join(h.env.Stock, "stages", "090-verify.toml")
	if err := os.WriteFile(edited, []byte("# mine\n"), 0o600); err != nil {
		t.Fatalf("editing: %v", err)
	}

	h.mustRun(t, "init", "--force")

	after, err := os.ReadFile(edited)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	if string(after) == "# mine\n" {
		t.Error("--force did not replace the edited file")
	}
}

// TestAProjectWithNoStockRunsTheShippedOne. `luna init` is opt-in: a repository
// that never customises anything never needs it.
func TestAProjectWithNoStockRunsTheShippedOne(t *testing.T) {
	if _, ok := ProjectStock(t.TempDir()); ok {
		t.Error("an empty directory was taken for a stock")
	}
	if _, ok := ProjectStock(""); ok {
		t.Error("no directory at all was taken for a stock")
	}

	// A directory with roles but no stages is not a stock either: roles have
	// working defaults, and a flow does not.
	partial := t.TempDir()
	if err := os.MkdirAll(filepath.Join(partial, "roles"), 0o750); err != nil {
		t.Fatalf("making a partial stock: %v", err)
	}
	if _, ok := ProjectStock(partial); ok {
		t.Error("a directory with no stages was taken for a stock")
	}
}

// TestFlowCheckSaysWhereTheFlowCameFrom. Editing a file changes what Luna runs,
// so "which files am I looking at" has to be answerable without guessing.
func TestFlowCheckSaysWhereTheFlowCameFrom(t *testing.T) {
	h := initHarness(t)

	shipped := h.mustRun(t, "flow", "check")
	if !strings.Contains(shipped, "luna init") {
		t.Errorf("a project with no stock is not told how to get one:\n%s", shipped)
	}

	h.mustRun(t, "init")
	own := h.mustRun(t, "flow", "check")
	if !strings.Contains(own, h.env.Stock) {
		t.Errorf("a project with its own stock is not told where it is:\n%s", own)
	}
}

func TestInitRefusesAFlagItDoesNotKnow(t *testing.T) {
	h := initHarness(t)

	if err := h.run(t, "init", "--nonsense"); err == nil {
		t.Fatal("an unknown flag was accepted")
	}
}

// TestInitNeedsSomewhereToWrite. An Env with no project directory is a test or a
// caller that has not been wired; either way, writing into the working directory
// would be a surprise.
func TestInitNeedsSomewhereToWrite(t *testing.T) {
	h := newHarness(t)
	h.env.Stock = ""

	if err := h.run(t, "init"); err == nil {
		t.Fatal("init wrote somewhere with no directory configured")
	}
}

// TestTheStockSitsBesideTheLog. Where a project's stock lives is a fact other
// things depend on — `luna init` writes it, main reads it, and a person looks
// for it.
func TestTheStockSitsBesideTheLog(t *testing.T) {
	got := StockDir("/repos/app/.luna/luna.db")

	if want := filepath.Join("/repos/app/.luna", "stock"); got != want {
		t.Errorf("StockDir = %s, want %s", got, want)
	}
}

// TestInitReportsAPlaceItCannotWrite. The failure is ordinary — a read-only
// checkout, a path someone mistyped — and it has to name the problem rather than
// half-write a stock.
func TestInitReportsAPlaceItCannotWrite(t *testing.T) {
	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatalf("making a read-only directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	h := newHarness(t)
	h.env.Stock = filepath.Join(locked, "stock")

	if err := h.run(t, "init"); err == nil {
		t.Skip("this filesystem allowed the write anyway")
	}

	// And nothing half-written is left behind for the next run to trip over.
	if entries, err := os.ReadDir(h.env.Stock); err == nil && len(entries) > 0 {
		t.Errorf("a failed init left %d entries behind", len(entries))
	}
}

// TestARoleFileThatCannotBeReadIsReported covers the directory being readable
// and a file in it not — rare, and the kind of thing that produces a role with
// half a definition if it is ignored.
func TestARoleFileThatCannotBeReadIsReported(t *testing.T) {
	dir := t.TempDir()
	roles := filepath.Join(dir, "roles")
	if err := os.MkdirAll(roles, 0o750); err != nil {
		t.Fatalf("making the directory: %v", err)
	}

	unreadable := filepath.Join(roles, "scout.toml")
	if err := os.WriteFile(unreadable, []byte("agent = \"claude\"\n"), 0o200); err != nil {
		t.Fatalf("writing it: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o600) })

	if _, err := LoadRoles(os.DirFS(dir), "roles"); err == nil {
		t.Skip("this filesystem allowed the read anyway")
	}
}

// TestAProfileFileThatCannotBeReadIsReported is the same case as the role one,
// on the other loader.
func TestAProfileFileThatCannotBeReadIsReported(t *testing.T) {
	dir := t.TempDir()
	profiles := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(profiles, 0o750); err != nil {
		t.Fatalf("making the directory: %v", err)
	}

	unreadable := filepath.Join(profiles, "turbo.toml")
	if err := os.WriteFile(unreadable, []byte("waits = []\n"), 0o200); err != nil {
		t.Fatalf("writing it: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o600) })

	if _, err := LoadProfiles(os.DirFS(dir), "profiles"); err == nil {
		t.Skip("this filesystem allowed the read anyway")
	}
}

// TestForceReplacesRatherThanOverwrites covers what `--force` promises.
//
// Its refusal message says it "replaces" the stock with the shipped one, and it
// only ever wrote over what it recognised. A stage the shipped flow no longer
// carries survived, so a project that ran `luna init --force` after upgrading
// kept running a stage Luna had removed — silently, because the leftover file is
// a valid stage and the loader reads whatever is in the directory.
//
// Measured after two stages were removed: `luna flow check` still reported 14.
func TestForceReplacesRatherThanOverwrites(t *testing.T) {
	h := newHarness(t)
	h.env.Stock = t.TempDir()

	h.mustRun(t, "init")

	// A stage that is not in the shipped flow, left behind by an older version.
	stale := filepath.Join(h.env.Stock, "stages", "999-gone.toml")
	if err := os.WriteFile(stale, []byte("id = \"gone\"\nproduces = [\"x\"]\n"), 0o600); err != nil {
		t.Fatalf("planting the stale stage: %v", err)
	}

	h.mustRun(t, "init", "--force")

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("a stage the shipped flow no longer carries survived --force: %v", err)
	}
}
