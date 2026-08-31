package cli

import (
	"fmt"
	"strings"

	"github.com/brunoomariano/luna/src/internal/node"
)

// whereCommand says what the working directory is already working on.
//
// It answers the question every other command has to ask before it can do
// anything, and it answers it out loud: which repository, which checkout, and —
// when the branch says so — which task and stage. Standing in a stage's worktree,
// that is the whole identity, and nothing had to be typed.
func whereCommand(env Env, _ []string) error {
	id, err := whereAmI(env)
	if err != nil {
		return err
	}

	line := func(k, v string) {
		if v != "" {
			fmt.Fprintf(env.Out, "  %-9s %s\n", k, v)
		}
	}
	line("repo", id.Repo)
	line("remote", id.RemoteURL)
	if id.Linked {
		line("worktree", id.Worktree)
	}
	line("branch", id.Branch)

	if id.TaskID == "" {
		fmt.Fprintf(env.Out, "\nthis checkout names no task — `luna <command> <id>` needs one\n")
		return nil
	}
	line("task", id.TaskID)
	line("stage", id.Stage)

	// The cross-check is printed rather than kept, because a disagreement is the
	// one thing a person standing here cannot see for themselves.
	if agrees, why := id.Agrees(); !agrees {
		fmt.Fprintf(env.Out, "\nthe checkout and its branch disagree:\n  %s\n", why)
	}
	return nil
}

// whereAmI resolves the working directory, or says plainly that it cannot.
//
// A command that falls back to this has already been given no id, so the failure
// it reports has to name both halves: nothing was typed and nothing could be
// inferred.
func whereAmI(env Env) (node.Identity, error) {
	if env.Where == nil {
		return node.Identity{}, fmt.Errorf("%w: no task id, and this build cannot tell "+
			"where it is running", ErrUsage)
	}
	return env.Where()
}

// taskFrom is the id an argument gave, or the one the directory already knows.
//
// The argument wins when there is one: somebody naming a task means that task,
// even standing somewhere else. What this removes is having to name it while
// standing in its own worktree, which is where every command was asking for
// something the directory could already answer.
func taskFrom(env Env, args []string) (Env, string, []string, error) {
	if len(args) > 0 && !isFlag(args[0]) {
		scoped, id, err := env.inProject(args[0])
		return scoped, id, args[1:], err
	}

	id, err := whereAmI(env)
	if err != nil {
		return Env{}, "", nil, err
	}
	if id.TaskID == "" {
		return Env{}, "", nil, fmt.Errorf("%w: no task id, and %s names none — "+
			"a checkout Luna made is on `luna/<task>/<stage>`", ErrUsage, id.Worktree)
	}

	// A disagreement is refused rather than resolved. Inferring an id is a
	// convenience, and a convenience that guesses which of two sources is right is
	// worse than asking.
	if agrees, why := id.Agrees(); !agrees {
		return Env{}, "", nil, fmt.Errorf("%w: this checkout is ambiguous — %s", ErrUsage, why)
	}
	return env, id.TaskID, args, nil
}

// inProject scopes a command to the project a reference names, or leaves it on
// the one the working directory is in.
//
// Every global listing prints `project/task`, and until now nothing accepted it
// back: a person reading `luna gates` had to work out which checkout a task
// belonged to and go there. A task in a project with no checkout at all — the
// ones a test run leaves behind — could never be touched again, and sat in every
// listing for the life of the machine.
//
// The separator is unambiguous by construction: a project key is a sanitised name
// matching `[a-zA-Z0-9._-]` plus a digest, so it holds no slash and a bare id
// cannot be mistaken for a qualified one.
//
// There is no guard for a missing global handle. One database holds every
// project and the scoped view is a filter over the same handle, so a command with
// one always has the other.
func (e Env) inProject(ref string) (Env, string, error) {
	project, id, qualified := strings.Cut(ref, "/")
	if !qualified {
		return e, ref, nil
	}
	if id == "" {
		return Env{}, "", fmt.Errorf("%w: %q names a project and no task", ErrUsage, ref)
	}
	scoped := e
	scoped.Store = e.GlobalStore.ForProject(project)
	return scoped, id, nil
}

// isFlag reports whether an argument is a flag rather than an id, so that
// `luna status --json` reads as "this task, as json" rather than as a task
// called `--json`.
func isFlag(arg string) bool {
	return len(arg) > 1 && arg[0] == '-'
}
