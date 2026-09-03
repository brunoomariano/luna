package verify_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/brunoomariano/luna/src/internal/contract"
	"github.com/brunoomariano/luna/src/internal/verify"
)

// repo is a real git repository. The measured defects in this package are all
// about what git answers for a commit that is missing, equal to its base, or
// unreadable — a fake with no git has no boundary to get any of that wrong, and
// three bugs in a row reached production behind exactly that kind of fake.
type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	r := &repo{t: t, dir: t.TempDir()}
	r.git("init", "--quiet", "-b", "main")
	r.git("config", "user.email", "test@example.com")
	r.git("config", "user.name", "Test")
	return r
}

func (r *repo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit writes a file and commits it, answering with the new sha.
func (r *repo) commit(path, body string) string {
	r.t.Helper()
	full := filepath.Join(r.dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		r.t.Fatal(err)
	}
	r.git("add", "-A")
	r.git("commit", "--quiet", "-m", "add "+path)
	return r.git("rev-parse", "HEAD")
}

func commandContract(phase, artifact, run string, scope contract.Scope) contract.Contract {
	return contract.Contract{
		Phase:    phase,
		Produces: map[string]contract.Verifier{artifact: contract.Command{Run: run, Scope: scope}},
	}
}

func proveOne(t *testing.T, s verify.Shell, c contract.Contract) verify.Evidence {
	t.Helper()
	all, err := s.Prove(context.Background(), c)
	if err != nil {
		t.Fatalf("proving: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected one piece of evidence, got %d", len(all))
	}
	return all[0]
}

func TestACommandThatPassesIsProvenAtTheScopeItDeclares(t *testing.T) {
	r := newRepo(t)
	head := r.commit("a.txt", "one")

	got := proveOne(t, verify.Shell{Dir: r.dir, Commit: head},
		commandContract("forge", "ci_green", "true", contract.ScopeFull))

	if got.Verdict != verify.VerdictPassed {
		t.Errorf("verdict: got %q, want passed (%s)", got.Verdict, got.Detail)
	}
	if got.Scope != contract.ScopeFull {
		t.Errorf("scope: got %q, want full", got.Scope)
	}
}

func TestACommandThatFailsIsAVerdictAboutTheWorkAndCarriesItsOutput(t *testing.T) {
	r := newRepo(t)
	head := r.commit("a.txt", "one")

	got := proveOne(t, verify.Shell{Dir: r.dir, Commit: head},
		commandContract("forge", "ci_green", "echo 'the suite is red' >&2; exit 3", contract.ScopeFull))

	if got.Verdict != verify.VerdictFailed {
		t.Fatalf("verdict: got %q, want failed", got.Verdict)
	}
	if got.ExitCode != 3 {
		t.Errorf("exit code: got %d, want 3", got.ExitCode)
	}
	if !strings.Contains(got.Detail, "the suite is red") {
		t.Errorf("the failing output did not travel with the evidence: %q", got.Detail)
	}
}

// INV-1, the case that got through: an empty commit resolved to the main
// repository's HEAD and the command ran over code the phase never wrote.
func TestAPhaseThatCommittedNothingDoesNotPassOnSomebodyElsesCode(t *testing.T) {
	r := newRepo(t)
	r.commit("a.txt", "already green here")

	got := proveOne(t, verify.Shell{Dir: r.dir}, // no Commit: nothing was delivered
		commandContract("forge", "ci_green", "true", contract.ScopeFull))

	if got.Verdict != verify.VerdictFailed {
		t.Fatalf("a phase that delivered nothing passed on the repository's own HEAD")
	}
	if !strings.Contains(got.Detail, "committed nothing") {
		t.Errorf("the detail does not say what was missing: %q", got.Detail)
	}
}

// The second half of the same case: a phase that adds no commit hands back its
// base, which exists and resolves, so the check ran over the code it was given.
func TestAPhaseThatDeliveredNothingNewIsNotProvenByItsBase(t *testing.T) {
	r := newRepo(t)
	base := r.commit("a.txt", "green when the phase received it")

	got := proveOne(t, verify.Shell{Dir: r.dir, Commit: base, Base: base},
		commandContract("forge", "ci_green", "true", contract.ScopeFull))

	if got.Verdict != verify.VerdictFailed {
		t.Fatal("a delivery equal to its base was accepted as a delivery")
	}
	if !strings.Contains(got.Detail, "on top of") {
		t.Errorf("the detail does not distinguish this from having no commit: %q", got.Detail)
	}
}

// A repository with no commit at all is the first phase of the first run. There
// is nothing to have delivered, and the working tree is all there is.
func TestTheFirstPhaseOfTheFirstRunVerifiesItsTree(t *testing.T) {
	r := newRepo(t)
	if err := os.WriteFile(filepath.Join(r.dir, "a.txt"), []byte("uncommitted"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := proveOne(t, verify.Shell{Dir: r.dir},
		commandContract("setup", "ready", "test -f a.txt", contract.ScopeTargeted))

	if got.Verdict != verify.VerdictPassed {
		t.Errorf("a repository with no commit was refused: %s", got.Detail)
	}
}

// The command runs over what was committed, not over what the agent left lying
// around. This is the incoherence INV-1 exists to close.
func TestTheCheckRunsOverTheDeliveryAndNotTheWorkingTree(t *testing.T) {
	r := newRepo(t)
	head := r.commit("a.txt", "committed")

	// Present in the tree, absent from the delivery.
	if err := os.WriteFile(filepath.Join(r.dir, "uncommitted.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := proveOne(t, verify.Shell{Dir: r.dir, Commit: head},
		commandContract("forge", "ci_green", "test ! -f uncommitted.txt", contract.ScopeFull))

	if got.Verdict != verify.VerdictPassed {
		t.Errorf("an uncommitted file was visible to the check: %s", got.Detail)
	}
}

func TestAnExistenceCheckWithAPathAsksGitRatherThanTheAgent(t *testing.T) {
	r := newRepo(t)
	head := r.commit("tests/test_tally.sh", "#!/bin/sh")

	c := contract.Contract{
		Phase:    "plan",
		Produces: map[string]contract.Verifier{"scenarios": contract.Existence{Path: "tests/"}},
	}
	got := proveOne(t, verify.Shell{Dir: r.dir, Commit: head}, c)

	if got.Verdict != verify.VerdictPassed {
		t.Fatalf("a committed file under the declared path was not found: %s", got.Detail)
	}
	if got.Scope != contract.ScopeExistence {
		t.Errorf("scope: got %q — finding a file proves existence and no more", got.Scope)
	}
}

func TestAnExistenceCheckFailsWhenNothingWasCommittedUnderThePath(t *testing.T) {
	r := newRepo(t)
	head := r.commit("src/main.go", "package main")

	c := contract.Contract{
		Phase:    "plan",
		Produces: map[string]contract.Verifier{"scenarios": contract.Existence{Path: "tests/"}},
	}
	got := proveOne(t, verify.Shell{Dir: r.dir, Commit: head}, c)

	if got.Verdict != verify.VerdictFailed {
		t.Error("an artifact nobody committed was reported as delivered")
	}
}

// An existence check with no path claims the agent's word and says so. It is the
// honest floor, and it does not fail for want of a commit.
func TestAnExistenceCheckWithNoPathClaimsOnlyThatItWasDelivered(t *testing.T) {
	r := newRepo(t)

	c := contract.Contract{
		Phase:    "plan",
		Produces: map[string]contract.Verifier{"approach": contract.Existence{}},
	}
	got := proveOne(t, verify.Shell{Dir: r.dir}, c)

	if got.Verdict != verify.VerdictPassed || got.Scope != contract.ScopeExistence {
		t.Errorf("got %s at %q, want passed at existence", got.Verdict, got.Scope)
	}
}

// A check that could not run is not a failing check. Recording it as one would
// tell whoever reads the record that the tests ran and lost.
func TestACheckThatCannotRunIsAnErrorRatherThanAFailedVerdict(t *testing.T) {
	r := newRepo(t)
	head := r.commit("a.txt", "one")

	s := verify.Shell{Dir: r.dir, Commit: head, Timeout: 50 * 1e6} // 50ms
	_, err := s.Prove(context.Background(), commandContract("forge", "ci_green", "sleep 5", contract.ScopeFull))

	if err == nil {
		t.Fatal("a command that never finished produced a verdict")
	}
	if !strings.Contains(err.Error(), "did not finish") {
		t.Errorf("the error does not say the check timed out: %v", err)
	}
}

func TestACancelledRunIsToldApartFromATimeout(t *testing.T) {
	r := newRepo(t)
	head := r.commit("a.txt", "one")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s := verify.Shell{Dir: r.dir, Commit: head}
	_, err := s.Prove(ctx, commandContract("forge", "ci_green", "true", contract.ScopeFull))
	if err == nil {
		t.Fatal("a cancelled run produced a verdict")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("a cancellation was reported as something else: %v", err)
	}
}

// Every owed artifact appears, passing ones included: a report listing only
// failures cannot be told from one where nothing ran.
func TestEveryOwedArtifactIsReportedInAStableOrder(t *testing.T) {
	r := newRepo(t)
	head := r.commit("a.txt", "one")

	c := contract.Contract{
		Phase: "forge",
		Produces: map[string]contract.Verifier{
			"ci_green": contract.Command{Run: "true", Scope: contract.ScopeFull},
			"code":     contract.Existence{},
		},
		ForHuman: map[string]contract.Verifier{
			"delivery_summary": contract.Existence{},
		},
	}

	all, err := verify.Shell{Dir: r.dir, Commit: head}.Prove(context.Background(), c)
	if err != nil {
		t.Fatalf("proving: %v", err)
	}

	var names []string
	for _, e := range all {
		names = append(names, e.Artifact)
	}
	if got := strings.Join(names, ","); got != "ci_green,code,delivery_summary" {
		t.Errorf("order: got %q", got)
	}
	if !verify.Passed(all) {
		t.Errorf("a contract whose checks all pass was not reported as passing: %v", verify.Failures(all))
	}
}

func TestFailuresCarryOnlyWhatDidNotPass(t *testing.T) {
	r := newRepo(t)
	head := r.commit("a.txt", "one")

	c := contract.Contract{
		Phase: "forge",
		Produces: map[string]contract.Verifier{
			"ci_green": contract.Command{Run: "exit 1", Scope: contract.ScopeFull},
			"code":     contract.Existence{},
		},
	}
	all, err := verify.Shell{Dir: r.dir, Commit: head}.Prove(context.Background(), c)
	if err != nil {
		t.Fatalf("proving: %v", err)
	}

	failed := verify.Failures(all)
	if len(failed) != 1 || failed[0].Artifact != "ci_green" {
		t.Fatalf("failures: got %+v", failed)
	}
	if verify.Passed(all) {
		t.Error("a contract with a failing check was reported as passing")
	}
}

// A directory that is not a repository is a caller checking a plain directory on
// purpose. Refusing would report a broken machine as a failed delivery.
func TestAPlainDirectoryIsCheckedRatherThanRefused(t *testing.T) {
	dir := t.TempDir()

	got := proveOne(t, verify.Shell{Dir: dir},
		commandContract("chore", "ran", "true", contract.ScopeTargeted))

	if got.Verdict != verify.VerdictPassed {
		t.Errorf("a plain directory was refused: %s", got.Detail)
	}
}

// The output that travels with a failure is bounded, and cut on a rune boundary
// so non-ASCII output does not leave broken UTF-8 in the record.
//
// The command is tuned so the 300-byte bound lands *inside* a two-byte rune:
// 60 of them after a single ASCII character, three lines joined. Without that
// the truncation path never runs, and an earlier version of this test passed
// against a `summarise` that sliced by raw byte index.
func TestAFailingCommandsDetailIsBoundedAndValidUTF8(t *testing.T) {
	r := newRepo(t)
	head := r.commit("a.txt", "one")

	got := proveOne(t, verify.Shell{Dir: r.dir, Commit: head},
		commandContract("forge", "ci_green",
			`for i in 1 2 3; do printf 'x'; for j in $(seq 1 60); do printf '\303\247'; done; `+
				`printf ' build failed\n'; done; exit 1`,
			contract.ScopeFull))

	if got.Verdict != verify.VerdictFailed {
		t.Fatal("expected a failure")
	}
	if len(got.Detail) > 300 {
		t.Errorf("the detail is %d bytes, past the 300-byte bound", len(got.Detail))
	}
	if len(got.Detail) < 250 {
		t.Fatalf("the detail is only %d bytes, so the truncation path never ran and this "+
			"test proves nothing", len(got.Detail))
	}
	if !utf8.ValidString(got.Detail) {
		t.Errorf("the detail was cut mid-rune: %q", got.Detail)
	}
}

// The branch travels with the work, and the directory is only a cross-check.
//
// One branch per run: a run has one worktree and walks several phases on it, so a
// phase in the name goes stale at the first transition. Both branches from the
// first real use say `intake` for runs that reached `pr`.
func TestACheckoutOnALunaBranchKnowsWhichRunItIs(t *testing.T) {
	r := newRepo(t)
	r.commit("a.txt", "one")
	r.git("checkout", "--quiet", "-b", "luna/MAX-2")

	where := verify.Identify(context.Background(), r.dir)
	if where.Run != "MAX-2" {
		t.Errorf("got run=%q from branch %q", where.Run, where.Branch)
	}
	if where.Phase != "" {
		t.Errorf("a branch with no phase reported one: %q", where.Phase)
	}
}

// The older shape still resolves — branches in it exist — but the phase it names
// is history rather than state.
func TestAnOlderPerPhaseBranchStillNamesItsRun(t *testing.T) {
	r := newRepo(t)
	r.commit("a.txt", "one")
	r.git("checkout", "--quiet", "-b", "luna/MAX-2/intake")

	where := verify.Identify(context.Background(), r.dir)
	if where.Run != "MAX-2" {
		t.Errorf("an older branch stopped naming its run: %+v", where)
	}
}

func TestABranchWithMoreThanTwoSegmentsNamesNoRun(t *testing.T) {
	r := newRepo(t)
	r.commit("a.txt", "one")
	r.git("checkout", "--quiet", "-b", "luna/a/b/c")

	if where := verify.Identify(context.Background(), r.dir); where.Run != "" {
		t.Errorf("a branch Luna does not define was read as a run: %q", where.Run)
	}
}

func TestAnOrdinaryBranchNamesNoRun(t *testing.T) {
	r := newRepo(t)
	r.commit("a.txt", "one")

	where := verify.Identify(context.Background(), r.dir)
	if where.Run != "" {
		t.Errorf("an ordinary branch was read as a run: %q", where.Run)
	}
	if where.Branch != "main" {
		t.Errorf("branch: got %q", where.Branch)
	}
}

// A directory that is not a repository still answers, because Luna is a tool
// somebody may run anywhere.
func TestIdentifyAnswersOutsideARepository(t *testing.T) {
	dir := t.TempDir()

	where := verify.Identify(context.Background(), dir)
	if where.Repo == "" || where.Worktree == "" {
		t.Errorf("identify gave up outside a repository: %+v", where)
	}
}

// The forms that have to land together are the ones a person actually has.
func TestOneRepositoryHasOneNameHoweverItIsSpelled(t *testing.T) {
	want := "github.com/me/app"
	for _, spelling := range []string{
		"git@github.com:me/app.git",
		"https://github.com/me/app",
		"https://github.com/me/app.git",
		"ssh://git@github.com/me/app.git",
		"https://github.com/Me/App/",
	} {
		if got := verify.NormalizeRemote(spelling); got != want {
			t.Errorf("%s → %q, want %q", spelling, got, want)
		}
	}
}

// The host is identity, not punctuation: two forges sharing a path are two
// projects, and merging them would file one project's runs under another's name.
func TestTwoForgesSharingAPathStayApart(t *testing.T) {
	a := verify.NormalizeRemote("git@github.com:me/app.git")
	b := verify.NormalizeRemote("git@gitlab.com:me/app.git")
	if a == b {
		t.Errorf("two hosts collapsed onto one name: %q", a)
	}
}

func TestTheProjectFallsBackToThePathWhenThereIsNoRemote(t *testing.T) {
	r := newRepo(t)
	r.commit("a.txt", "one")

	where := verify.Identify(context.Background(), r.dir)
	if where.Project == "" {
		t.Error("a repository with no remote has no project name")
	}
}

func TestHeadOfResolvesTheCurrentCommit(t *testing.T) {
	r := newRepo(t)
	want := r.commit("a.txt", "one")

	got, err := verify.HeadOf(context.Background(), r.dir)
	if err != nil {
		t.Fatalf("resolving HEAD: %v", err)
	}
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestHeadOfAnEmptyRepositoryIsAnError(t *testing.T) {
	r := newRepo(t)

	if _, err := verify.HeadOf(context.Background(), r.dir); err == nil {
		t.Error("a repository with no commit resolved a HEAD")
	}
}

// A delivery checkout is thrown away, and it leaves no worktree registration
// behind — one that outlived the run would accumulate in the repository.
func TestADeliveryCheckoutLeavesNothingBehind(t *testing.T) {
	r := newRepo(t)
	head := r.commit("a.txt", "one")

	delivered, err := verify.CheckoutAt(context.Background(), r.dir, head)
	if err != nil {
		t.Fatalf("checking out: %v", err)
	}
	if delivered == nil {
		t.Fatal("nothing was checked out")
	}
	path := delivered.Path
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the checkout is not there: %v", err)
	}

	delivered.Close()
	delivered.Close() // safe more than once

	if _, err := os.Stat(path); err == nil {
		t.Error("the checkout outlived its Close")
	}
	if out := r.git("worktree", "list"); strings.Contains(out, path) {
		t.Errorf("a worktree registration was left behind:\n%s", out)
	}
}

func TestCheckingOutARepositoryWithNoCommitAnswersNothing(t *testing.T) {
	r := newRepo(t)

	delivered, err := verify.CheckoutAt(context.Background(), r.dir, "")
	if err != nil {
		t.Fatalf("an empty repository was an error: %v", err)
	}
	if delivered != nil {
		delivered.Close()
		t.Error("something was checked out of a repository with no commits")
	}
}

// A path that is not committed at all, on a phase with no commit: there is
// nowhere to look, so nothing is claimed either way.
func TestAPathCheckedBeforeAnythingIsDeliveredClaimsNothing(t *testing.T) {
	r := newRepo(t)

	c := contract.Contract{
		Phase:    "plan",
		Produces: map[string]contract.Verifier{"scenarios": contract.Existence{Path: "tests/"}},
	}
	got := proveOne(t, verify.Shell{Dir: r.dir}, c)

	if got.Verdict != verify.VerdictPassed {
		t.Errorf("a path check before the first delivery failed: %s", got.Detail)
	}
	if !strings.Contains(got.Detail, "nothing to look in") {
		t.Errorf("the detail does not say why: %q", got.Detail)
	}
}

// A commit that cannot be read is a broken repository, not a missing artifact —
// blaming the agent for it would send work back for nothing.
func TestATreeThatCannotBeReadIsAnErrorRatherThanAnAbsentArtifact(t *testing.T) {
	r := newRepo(t)
	r.commit("a.txt", "one")

	c := contract.Contract{
		Phase:    "plan",
		Produces: map[string]contract.Verifier{"scenarios": contract.Existence{Path: "tests/"}},
	}
	_, err := verify.Shell{Dir: r.dir, Commit: "0000000000000000000000000000000000000000"}.
		Prove(context.Background(), c)

	if err == nil {
		t.Fatal("an unreadable tree was reported as a missing artifact")
	}
}

// OverWorkingTree says outright that the tree is the subject, which is what a
// check declared against work in progress wants.
func TestAWorkingTreeCheckSaysSoRatherThanCheckingOutACommit(t *testing.T) {
	r := newRepo(t)
	r.commit("a.txt", "committed")
	if err := os.WriteFile(filepath.Join(r.dir, "wip.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := proveOne(t, verify.Shell{Dir: r.dir, OverWorkingTree: true},
		commandContract("forge", "seen", "test -f wip.txt", contract.ScopeTargeted))

	if got.Verdict != verify.VerdictPassed {
		t.Errorf("an uncommitted file was invisible to a working-tree check: %s", got.Detail)
	}
}

// A passing check needs no detail: the exit code says it, and a build log in a
// ledger line makes the whole record unreadable.
func TestAPassingCommandCarriesNoOutput(t *testing.T) {
	r := newRepo(t)
	head := r.commit("a.txt", "one")

	got := proveOne(t, verify.Shell{Dir: r.dir, Commit: head},
		commandContract("forge", "ci_green", "echo lots of noise", contract.ScopeFull))

	if got.Detail != "" {
		t.Errorf("a passing check carried output: %q", got.Detail)
	}
}

func TestAShortCommitIsNotTruncatedFurther(t *testing.T) {
	r := newRepo(t)
	base := r.commit("a.txt", "one")

	got := proveOne(t, verify.Shell{Dir: r.dir, Commit: base, Base: base},
		commandContract("forge", "ci_green", "true", contract.ScopeFull))

	if !strings.Contains(got.Detail, base[:12]) {
		t.Errorf("the detail does not name the base it was handed: %q", got.Detail)
	}
}
