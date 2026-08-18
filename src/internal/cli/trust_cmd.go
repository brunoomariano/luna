package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/brunoomariano/luna/src/internal/node"
)

// trustCommand records the worktree parent as trusted in the user's claude
// configuration, so agents stop meeting the folder-trust dialog.
//
// It is a command a person runs, once per machine and repository layout — never
// something Luna does on its own. The file it edits holds the user's
// credentials, and the difference between "the user asked for this edit" and
// "Luna decided to make it" is the whole lesson of the incident that shaped
// node.TrustWorktreeParent.
func trustCommand(env Env, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%w: luna trust takes no arguments — it trusts where this repository's worktrees are made",
			ErrUsage)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("finding the home directory: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("finding the working directory: %w", err)
	}
	repo, err := node.Root(context.Background(), cwd)
	if err != nil {
		return err
	}

	parent, err := node.TrustWorktreeParent(home, repo)
	if err != nil {
		return err
	}

	fmt.Fprintf(env.Out, "claude now trusts %s\n", parent)
	fmt.Fprintf(env.Out, "  every worktree Luna makes for this repository is created there, so its agents\n")
	fmt.Fprintf(env.Out, "  start at a prompt instead of at the folder-trust dialog\n")
	return nil
}
