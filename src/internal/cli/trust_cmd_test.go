package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestTrustCommandTrustsTheWorktreeParent drives the command end to end with a
// controlled home, because the file it edits is the user's real claude
// configuration and no test may ever touch that one.
func TestTrustCommandTrustsTheWorktreeParent(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"theme":"dark"}`), 0o600); err != nil {
		t.Fatalf("setting up the config: %v", err)
	}
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatalf("setting up the repository: %v", err)
	}

	t.Setenv("HOME", home)
	t.Chdir(repo)

	h := newHarness(t)
	if err := Run(h.env, []string{"trust"}); err != nil {
		t.Fatalf("trusting: %v", err)
	}

	out := h.out.String()
	if !strings.Contains(out, "claude now trusts") {
		t.Errorf("the command must say what it did, got %q", out)
	}
	raw, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if !strings.Contains(string(raw), "hasTrustDialogAccepted") {
		t.Errorf("the trust must be recorded, got %s", raw)
	}
	if !strings.Contains(string(raw), `"theme":"dark"`) {
		t.Errorf("everything else survives, got %s", raw)
	}
}

// TestTrustCommandTakesNoArguments pins the surface: what to trust is derived,
// not chosen, so an argument is a misunderstanding to correct.
func TestTrustCommandTakesNoArguments(t *testing.T) {
	h := newHarness(t)

	err := Run(h.env, []string{"trust", "/somewhere"})
	if err == nil || !strings.Contains(err.Error(), "takes no arguments") {
		t.Errorf("an argument must be refused with the reason, got %v", err)
	}
}

// TestTrustCommandRelaysAMissingConfig covers the refusal travelling up whole:
// the person needs the instruction, not just a failure.
func TestTrustCommandRelaysAMissingConfig(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatalf("setting up: %v", err)
	}
	t.Setenv("HOME", home)
	t.Chdir(repo)

	h := newHarness(t)
	err := Run(h.env, []string{"trust"})
	if err == nil || !strings.Contains(err.Error(), "run claude once") {
		t.Errorf("the refusal must carry the way out, got %v", err)
	}
}

// TestTrustCommandReportsAHomelessProcess covers the environment nothing else
// can reach: no HOME means no config to find.
func TestTrustCommandReportsAHomelessProcess(t *testing.T) {
	t.Setenv("HOME", "")

	h := newHarness(t)
	if err := Run(h.env, []string{"trust"}); err == nil {
		t.Error("a process with no home directory must be reported")
	}
}

// TestKnobNoteNamesEachRegime pins the three autonomy readings a person sees on
// `task show` — the middle one had no test and the note is what explains the
// number.
func TestKnobNoteNamesEachRegime(t *testing.T) {
	for knob, want := range map[fsm.Knob]string{
		fsm.KnobAsk: "every gate goes to a person",
		fsm.KnobAll: "judge every gate",
		5:           "needing this much autonomy or less",
	} {
		if got := knobNote(knob); !strings.Contains(got, want) {
			t.Errorf("knob %d: want %q in %q", knob, want, got)
		}
	}
}

// TestAConfiguredEnvKeepsItsOwnProfiles pins profiles()'s other branch: a
// project that defines any profile is taken at its word, shipped defaults and
// all their roles left out of it.
func TestAConfiguredEnvKeepsItsOwnProfiles(t *testing.T) {
	env := Env{Config: Config{Profiles: map[fsm.Profile]bool{"night-shift": true}}}

	cfg := env.profiles()
	if !cfg.Defines("night-shift") {
		t.Error("a project's own profile survives")
	}
	if cfg.Defines(fsm.ProfileTurbo) {
		t.Error("shipped names are the fallback, not an addition")
	}
}

// TestUndefinedProfileNoteWarnsOnlyWhereItShould: no note for no profile, none
// for a defined one, a warning for a name nothing defines any more.
func TestUndefinedProfileNoteWarnsOnlyWhereItShould(t *testing.T) {
	cfg := Config{Profiles: map[fsm.Profile]bool{"day": true}}

	if got := undefinedProfileNote(cfg, ""); got != "" {
		t.Errorf("no profile, no note, got %q", got)
	}
	if got := undefinedProfileNote(cfg, "day"); got != "" {
		t.Errorf("a defined profile needs no warning, got %q", got)
	}
	if got := undefinedProfileNote(cfg, "night"); !strings.Contains(got, "no longer defined") {
		t.Errorf("an undefined profile is warned about, got %q", got)
	}
}
