package node

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// MergeVerdict is what a merge attempt concluded. Two values, and neither of
// them is "resolved it for you".
type MergeVerdict string

const (
	// MergeReady means the branch merges cleanly and the merge was performed.
	MergeReady MergeVerdict = "merge_ready"

	// MergeBlocked means it does not. The conflict is named and nothing was
	// merged; the main repository is exactly as it was.
	MergeBlocked MergeVerdict = "merge_blocked"
)

// Merge is the result of integrating one stage's work.
type Merge struct {
	Verdict MergeVerdict

	// Commit is what the merge produced, on MergeReady. It becomes the next
	// stage's base.
	Commit string

	// Conflicts are the paths git could not reconcile, on MergeBlocked. They are
	// listed rather than summarised because the whole point of blocking is that
	// someone has to look, and "there was a conflict" does not tell them where.
	//
	// swarm-forge's own bugs.md records the failure this avoids: a multi-hour
	// stall where the dashboard never surfaced *which* file conflicted.
	Conflicts []string

	// Detail is what git said, for the evidence to carry.
	Detail string
}

// ErrNotTheOwner is returned when something other than Luna tries to merge.
//
// swarm-forge enforces the same rule with an exit code rather than a line in a
// prompt, and the reason is worth repeating: the shared git has exactly one
// owner, and a convention that only the prompt knows about is a convention that
// holds until an agent improvises. Here the owner is a field that has to be set,
// so the check is at the boundary rather than in the instructions.
var ErrNotTheOwner = errors.New("only Luna merges into the shared repository")

// Owner is who a merge is attempted on behalf of.
type Owner string

// LunaOwnsTheMerge is the only value that may merge.
const LunaOwnsTheMerge Owner = "luna"

// Merger integrates delivered work into the shared repository.
//
// It is a type rather than a function because merging has an owner, and the
// owner is Luna. Every agent works in its own worktree and commits there;
// nothing else touches the shared branch (RFC-0002, INV-core-4).
//
// This is deliberately not a model's job. swarm-forge left `merge_and_process`
// as prose in a prompt, defined nowhere, and their issue #29 records an agent
// passing the phrase to Bash and stopping on `command not found`. A fork
// documented the worse outcome: `git merge -X theirs`, and an agent silently
// discarding its own work. `git merge` is decidable, so Luna decides it.
type Merger struct {
	// Repo is the shared repository — the one thing with a single owner.
	Repo string

	// Branch is what work is merged into. Empty means whatever Repo has checked
	// out, which is the ordinary case.
	Branch string

	// As is who is merging. It has to be LunaOwnsTheMerge, and the field exists
	// precisely so that it cannot default to it: a zero value that meant "Luna"
	// would make the ownership rule true by accident, and a rule that holds by
	// accident is one a refactor can remove without a test noticing.
	//
	// The dry run does not require it — checking whether something *would* merge
	// touches nothing, and anyone may ask.
	As Owner
}

// authorised reports whether this merger may write to the shared repository.
func (m Merger) authorised() error {
	if m.As != LunaOwnsTheMerge {
		return fmt.Errorf("%w: %q asked to merge into %s", ErrNotTheOwner, m.As, m.Repo)
	}
	return nil
}

// DryRun reports whether a commit would merge cleanly, without touching the
// shared repository.
//
// The check runs in a throwaway worktree, which is the whole design: a conflict
// becomes a verdict rather than a repository left mid-merge for someone to find.
// swarm-forge arrived at the same shape after the alternative bit them, and this
// follows it deliberately rather than by coincidence.
//
// The verdict is produced outside the engine and travels into the action, which
// is [ADR-0024] applied to merging: the reducer decides what `merge_blocked`
// means, it does not go looking.
func (m Merger) DryRun(ctx context.Context, commit string) (Merge, error) {
	scratch, err := m.scratch(ctx)
	if err != nil {
		return Merge{}, err
	}
	defer scratch.Close()

	// --no-commit and --no-ff: the question is whether it merges, and a
	// fast-forward would answer a different one — that there was nothing to
	// reconcile — while leaving no merge to inspect.
	out, err := git(ctx, scratch.Path, "merge", "--no-commit", "--no-ff", commit)
	if err == nil {
		return Merge{Verdict: MergeReady, Detail: firstLine(out)}, nil
	}

	conflicts, listErr := m.conflictedPaths(ctx, scratch.Path)
	if listErr != nil {
		// The merge failed and the reason cannot be read. Blocking on what git
		// said is still better than reporting ready.
		return Merge{Verdict: MergeBlocked, Detail: firstLine(err.Error())}, nil
	}

	return Merge{
		Verdict:   MergeBlocked,
		Conflicts: conflicts,
		Detail:    describeConflict(conflicts),
	}, nil
}

// Merge integrates the commit, but only after the dry run said it would work.
//
// The two are separate calls on purpose. Attempting the real merge to find out
// whether it merges leaves the shared repository mid-conflict on failure, and
// something then has to unwind it — which is a second thing that can go wrong at
// the exact moment the first one already did.
//
// There is no conflict resolution here, and the omission is the decision. A
// search of swarm-forge's branch for `-X theirs`, `--ours` and `rerere` returns
// nothing; having watched an agent discard its own work in silence, they refused
// the shortcut, and so does this. A conflict is a verdict, and resolving it is a
// stage with a contract.
func (m Merger) Merge(ctx context.Context, commit, message string) (Merge, error) {
	if err := m.authorised(); err != nil {
		return Merge{}, err
	}

	dry, err := m.DryRun(ctx, commit)
	if err != nil {
		return Merge{}, err
	}
	if dry.Verdict != MergeReady {
		return dry, nil
	}

	if _, err := git(ctx, m.Repo, "merge", "--no-ff", "-m", message, commit); err != nil {
		// The dry run passed and the real merge did not, which means the branch
		// moved between the two. Reporting blocked is the honest answer: the
		// verdict was taken against a state that no longer holds.
		return Merge{
			Verdict: MergeBlocked,
			Detail:  "the branch moved between the check and the merge: " + firstLine(err.Error()),
		}, nil
	}

	head, err := git(ctx, m.Repo, "rev-parse", "HEAD")
	if err != nil {
		return Merge{}, fmt.Errorf("reading the merge commit: %w", err)
	}
	return Merge{Verdict: MergeReady, Commit: head, Detail: "merged"}, nil
}

// scratch makes the throwaway worktree the dry run happens in.
//
// It is a detached checkout of the target branch, so the merge is attempted
// against exactly what would be merged into, and discarded afterwards whatever
// the outcome.
func (m Merger) scratch(ctx context.Context) (*Delivered, error) {
	target := m.Branch
	if target == "" {
		target = "HEAD"
	}

	head, err := git(ctx, m.Repo, "rev-parse", "--verify", target+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("resolving %s to merge into: %w", target, err)
	}

	return throwawayCheckout(ctx, m.Repo, head, "luna-merge-check-")
}

// conflictedPaths asks git which files it could not reconcile.
func (m Merger) conflictedPaths(ctx context.Context, worktree string) ([]string, error) {
	out, err := git(ctx, worktree, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	return strings.Split(strings.TrimSpace(out), "\n"), nil
}

func describeConflict(paths []string) string {
	switch len(paths) {
	case 0:
		return "the merge failed without naming a conflicted file"
	case 1:
		return "merge conflict on " + paths[0]
	default:
		return fmt.Sprintf("merge conflict on %s and %d more", paths[0], len(paths)-1)
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
