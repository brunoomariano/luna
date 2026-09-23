package cli_test

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/cli"
	"github.com/brunoomariano/luna/src/internal/skills"
)

// A skill that documents a verb the binary does not answer costs somebody a
// session, and the two drift the moment nobody checks. This is why the skill is
// embedded rather than shipped beside the binary — and embedding only helps if
// something ties the claims to the code.
//
// Every `luna <verb>` the skill names has to be a verb this build dispatches.
func TestEveryVerbTheSkillNamesIsOneTheBinaryAnswers(t *testing.T) {
	skill, err := skills.Find("luna")
	if err != nil {
		t.Fatal(err)
	}
	body := string(skill.Files["SKILL.md"])

	named := map[string]bool{}
	for _, m := range regexp.MustCompile(`luna ([a-z][a-z-]+)`).FindAllStringSubmatch(body, -1) {
		named[m[1]] = true
	}
	if len(named) < 5 {
		t.Fatalf("only %d verbs found in the skill; the pattern stopped matching", len(named))
	}

	// Prose reads "luna help is authoritative" and the like; these are not verbs
	// under test, and `help` is answered anyway. "is" comes from the ledger's
	// own refusal, quoted verbatim: the path ~/.local/share/luna ends a sentence
	// that carries on "is in memory". Quoting the binary's real message is worth
	// more than rewording it to satisfy this pattern.
	notVerbs := map[string]bool{"binary": true, "check--contract": true, "is": true}

	for verb := range named {
		if notVerbs[verb] {
			continue
		}
		h := newHarness(t)
		err := h.run(verb, "--help")
		if errors.Is(err, cli.ErrUsage) && strings.Contains(err.Error(), "unknown command") {
			t.Errorf("the skill tells an agent to run `luna %s`, and this build has no such verb", verb)
		}
	}

	// Dispatching is not enough. `report` still answers, as an undocumented
	// compatibility alias, so a skill that names it would pass the check above
	// while teaching a name that is on its way out. A verb the help does not
	// document is a verb the skill must not teach.
	h := newHarness(t)
	if err := h.run("help"); err != nil {
		t.Fatal(err)
	}
	help := h.stdout()
	for verb := range named {
		if notVerbs[verb] || verb == "help" {
			continue
		}
		if !strings.Contains(help, "luna "+verb) {
			t.Errorf("the skill teaches `luna %s`, which `luna help` does not document", verb)
		}
	}
}

// The skill states the closed sets and the exit codes. Those are the facts an
// agent acts on without checking, so a stale one is acted on confidently.
func TestTheSkillsFactsMatchTheBinary(t *testing.T) {
	skill, err := skills.Find("luna")
	if err != nil {
		t.Fatal(err)
	}
	body := string(skill.Files["SKILL.md"])

	for _, event := range []string{"phase", "gate", "block", "unblock", "autonomy", "discovery"} {
		if !strings.Contains(body, event) {
			t.Errorf("the skill does not name the %q event", event)
		}
	}
	for _, status := range []string{
		"running", "awaiting_gate", "awaiting_resume", "blocked", "done", "abandoned",
	} {
		if !strings.Contains(body, status) {
			t.Errorf("the skill does not name the %q status", status)
		}
	}
	for _, scope := range []string{"full", "targeted", "human", "existence"} {
		if !strings.Contains(body, scope) {
			t.Errorf("the skill does not name the %q scope", scope)
		}
	}
}

// The skill is meant to travel to machines that have none of this house's tools.
// A reference to one is a dead end for everybody else.
func TestTheSkillNamesNothingOutsideLuna(t *testing.T) {
	skill, err := skills.Find("luna")
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ToLower(string(skill.Files["SKILL.md"]))

	for _, houseOnly := range []string{
		"lsh-", "ai-run", "ai-jail", "ai-memory", "plane", "herdr", "dotfiles",
	} {
		if strings.Contains(body, houseOnly) {
			t.Errorf("the skill names %q, which does not exist on a machine that is not this one",
				houseOnly)
		}
	}
}

// The same rule, held against what the binary says rather than what it carries.
//
// The skill was checked and the briefing was not, so the briefing kept naming a
// skill from two renames ago: Luna installed `luna` and then told the agent to
// conduct with something no machine had. A rule enforced on the travelling
// document and not on the binary that travels with it is half a rule.
func TestTheBriefingNamesNothingOutsideLuna(t *testing.T) {
	h := newHarness(t)
	h.commit("a.txt", "one")

	if err := h.run("session", "claude", "--print"); err != nil {
		t.Fatal(err)
	}
	briefing := strings.ToLower(h.stdout())

	for _, houseOnly := range []string{
		"lsh-", "ai-run", "ai-jail", "ai-memory", "plane", "herdr", "dotfiles",
	} {
		if strings.Contains(briefing, houseOnly) {
			t.Errorf("the briefing names %q, which does not exist on a machine that is not this one:\n%s",
				houseOnly, h.stdout())
		}
	}
}

// The skill the briefing names by default has to be one Luna can actually
// produce. Naming a skill nobody has sends the agent looking for a file that
// `install-skills` never writes.
func TestTheBriefingNamesTheSkillLunaInstalls(t *testing.T) {
	h := newHarness(t)
	h.commit("a.txt", "one")

	if err := h.run("session", "claude", "--print"); err != nil {
		t.Fatal(err)
	}

	carried, err := skills.Find("luna")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.stdout(), carried.Name) {
		t.Errorf("the briefing does not name the skill this build carries (%q):\n%s",
			carried.Name, h.stdout())
	}
}

// Installing twice has to leave what installing once did. An install that
// refuses the second time is an install nobody runs the second time.
func TestInstallingSkillsTwiceLeavesTheSameThing(t *testing.T) {
	h := newHarness(t)
	into := filepath.Join(t.TempDir(), "skills")

	for round := 1; round <= 2; round++ {
		h.out.Reset()
		if err := h.run("install-skills", "--dir", into); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}

	installed := filepath.Join(into, "luna", "SKILL.md")
	written, err := os.ReadFile(installed)
	if err != nil {
		t.Fatalf("the skill is not where it was said to be: %v", err)
	}

	skill, err := skills.Find("luna")
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != string(skill.Files["SKILL.md"]) {
		t.Error("what was installed is not what this build carries")
	}
}

// A stale copy is worse than none: the agent reads it and acts on a verb that
// no longer exists.
func TestInstallingOverAnOlderCopyReplacesIt(t *testing.T) {
	h := newHarness(t)
	into := filepath.Join(t.TempDir(), "skills")
	stale := filepath.Join(into, "luna", "SKILL.md")

	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("# an older skill that says `luna report`\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := h.run("install-skills", "--dir", into); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(stale)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), "an older skill") {
		t.Error("an older copy survived the install")
	}
}

// Writing a tree into somebody's home for a harness that will never read it is
// worse than refusing: nothing reports the mistake.
func TestAnUnknownHarnessIsRefusedWithWhatToDo(t *testing.T) {
	h := newHarness(t)

	err := h.run("install-skills", "some-other-agent")
	if !errors.Is(err, cli.ErrUsage) {
		t.Fatalf("an unknown harness was accepted: %v", err)
	}
	for _, want := range []string{"some-other-agent", "claude", "codex", "--dir"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

func TestADryRunInstallsNothing(t *testing.T) {
	h := newHarness(t)
	into := filepath.Join(t.TempDir(), "skills")

	if err := h.run("install-skills", "--dir", into, "--dry-run"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.stdout(), "SKILL.md") {
		t.Errorf("a dry run does not say what it would write:\n%s", h.stdout())
	}
	if _, err := os.Stat(filepath.Join(into, "luna")); !os.IsNotExist(err) {
		t.Error("a dry run wrote something")
	}
}

// --print is for somebody who would rather read the skill, or place it
// themselves. It has to be the whole document, not a summary of it.
func TestPrintingTheSkillsWritesTheWholeDocument(t *testing.T) {
	h := newHarness(t)

	if err := h.run("install-skills", "--print"); err != nil {
		t.Fatal(err)
	}
	skill, err := skills.Find("luna")
	if err != nil {
		t.Fatal(err)
	}
	// Every file, whole. A skill carrying references prints them under a header
	// each, so the check is that nothing was summarised or left out — not that
	// the output equals any one file.
	for name, body := range skill.Files {
		if !strings.Contains(h.stdout(), string(body)) {
			t.Errorf("--print did not write %s as this build carries it", name)
		}
		if len(skill.Files) > 1 && !strings.Contains(h.stdout(), "luna/"+name) {
			t.Errorf("--print does not say which file %s is", name)
		}
	}
}

// --print names no destination, so it must not need one: the point is to see the
// skill without deciding where it goes.
func TestPrintingNeedsNoHarness(t *testing.T) {
	h := newHarness(t)

	if err := h.run("install-skills", "--print"); err != nil {
		t.Fatalf("--print demanded a harness: %v", err)
	}
}

// Naming both a harness and a directory is a question with no answer, and
// picking one silently is how somebody installs into a path they did not mean.
func TestAHarnessAndADirectoryTogetherAreRefused(t *testing.T) {
	h := newHarness(t)

	err := h.run("install-skills", "claude", "--dir", t.TempDir())
	if !errors.Is(err, cli.ErrUsage) {
		t.Fatalf("two destinations were accepted: %v", err)
	}
}

// The whole point of naming a harness is not having to know where it keeps
// skills. Every other test passes --dir, so without this one the resolution that
// a person actually uses is never run.
func TestAKnownHarnessResolvesItsOwnDirectory(t *testing.T) {
	h := newHarness(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	for harness, under := range map[string]string{
		"claude": filepath.Join(".claude", "skills"),
		"codex":  filepath.Join(".codex", "skills"),
	} {
		h.out.Reset()
		if err := h.run("install-skills", harness, "--dry-run"); err != nil {
			t.Fatalf("%s: %v", harness, err)
		}
		want := filepath.Join(home, under, "luna", "SKILL.md")
		if !strings.Contains(h.stdout(), want) {
			t.Errorf("%s does not resolve to %s:\n%s", harness, want, h.stdout())
		}
	}
}

// Installing for real into a resolved harness directory, since --dry-run proves
// the path and not the write.
func TestAKnownHarnessIsInstalledInto(t *testing.T) {
	h := newHarness(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := h.run("install-skills", "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", "luna", "SKILL.md")); err != nil {
		t.Errorf("nothing was installed where codex would look: %v", err)
	}
}

// Naming no destination at all must say what the choices are, rather than
// picking one — installing into a harness somebody does not use is a directory
// they will never find.
func TestInstallingWithNoDestinationNamesTheChoices(t *testing.T) {
	h := newHarness(t)

	err := h.run("install-skills")
	if !errors.Is(err, cli.ErrUsage) {
		t.Fatalf("install-skills with no destination was accepted: %v", err)
	}
	for _, want := range []string{"claude", "codex", "--dir"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not offer %q: %v", want, err)
		}
	}
}

// A failed write has to name the file. "permission denied" with no path is a
// debugging session.
func TestAWriteThatCannotHappenNamesTheFile(t *testing.T) {
	h := newHarness(t)
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := h.run("install-skills", "--dir", blocked)
	if err == nil {
		t.Fatal("installing into a path blocked by a file reported success")
	}
	if !strings.Contains(err.Error(), "luna") {
		t.Errorf("the failure does not name what it was writing: %v", err)
	}
}

// The help text names the default skill, and named it wrongly for two renames:
// it advertised a skill the binary had stopped using, which is the same defect
// as the briefing itself carrying a dead name. What the help says the default is
// has to be what the briefing produces.
func TestTheHelpNamesTheDefaultSkillTheBriefingUses(t *testing.T) {
	h := newHarness(t)
	h.commit("a.txt", "one")

	if err := h.run("session", "claude", "--print"); err != nil {
		t.Fatal(err)
	}
	briefing := h.stdout()

	h.out.Reset()
	if err := h.run("help"); err != nil {
		t.Fatal(err)
	}
	carried, err := skills.Find("luna")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(h.stdout(), "default "+carried.Name) {
		t.Errorf("the help does not name %q as the default skill", carried.Name)
	}
	if !strings.Contains(briefing, carried.Name) {
		t.Errorf("the briefing does not name %q, which the help promises", carried.Name)
	}
}
