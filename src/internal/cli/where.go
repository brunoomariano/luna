package cli

import (
	"fmt"

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
func taskFrom(env Env, args []string) (string, []string, error) {
	if len(args) > 0 && !isFlag(args[0]) {
		return args[0], args[1:], nil
	}

	id, err := whereAmI(env)
	if err != nil {
		return "", nil, err
	}
	if id.TaskID == "" {
		return "", nil, fmt.Errorf("%w: no task id, and %s names none — "+
			"a checkout Luna made is on `luna/<task>/<stage>`", ErrUsage, id.Worktree)
	}

	// A disagreement is refused rather than resolved. Inferring an id is a
	// convenience, and a convenience that guesses which of two sources is right is
	// worse than asking.
	if agrees, why := id.Agrees(); !agrees {
		return "", nil, fmt.Errorf("%w: this checkout is ambiguous — %s", ErrUsage, why)
	}
	return id.TaskID, args, nil
}

// isFlag reports whether an argument is a flag rather than an id, so that
// `luna status --json` reads as "this task, as json" rather than as a task
// called `--json`.
func isFlag(arg string) bool {
	return len(arg) > 1 && arg[0] == '-'
}
