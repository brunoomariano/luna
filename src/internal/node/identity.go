package node

import (
	"context"
	"fmt"
)

// CommitIdentity is who a stage's commits are authored and committed by.
//
// It exists because the sandbox does not carry one. The agent's `git` runs with
// the jail's `$HOME`, where the host's `~/.gitconfig` is absent — and on a
// machine where that file is a symlink into a dotfiles repository, mapping the
// path in does not help either, because the link resolves to somewhere else the
// jail cannot see. So the identity travels as environment, which git reads
// before any config file.
//
// Carrying it this way has a second effect worth stating, because it is a
// silent one: `GIT_AUTHOR_*` and `GIT_COMMITTER_*` bypass the global config
// entirely, so a `commit.gpgsign = true` there does not apply to a stage's
// commits. That is the outcome we want — signing inside the jail would need the
// signing key inside the jail — but it means a stage's commits are unsigned
// even on a machine that signs everything.
type CommitIdentity struct {
	Name  string
	Email string
}

// Env renders the identity as the four variables git reads.
//
// Author and committer are set to the same person: the agent is both, and
// leaving the committer unset would send git looking for the config that is not
// there — the exact failure this type exists to prevent.
func (c CommitIdentity) Env() []string {
	return []string{
		"GIT_AUTHOR_NAME=" + c.Name,
		"GIT_AUTHOR_EMAIL=" + c.Email,
		"GIT_COMMITTER_NAME=" + c.Name,
		"GIT_COMMITTER_EMAIL=" + c.Email,
	}
}

// ReadCommitIdentity asks the repository who its commits belong to.
//
// Read from the repository rather than from Luna's own configuration so a task
// commits as whoever owns the checkout, and read outside the sandbox because
// that is the only place the answer exists.
//
// A missing name or email is an error rather than a default. An agent that
// cannot commit cannot deliver, and a stage that starts anyway spends a model's
// budget to produce nothing — which is exactly what five stages of TALLY-5 did
// for a neighbouring reason. Failing here costs a second; failing at the
// handover costs the stage.
func ReadCommitIdentity(ctx context.Context, repo string) (CommitIdentity, error) {
	name, err := git(ctx, repo, "config", "user.name")
	if err != nil {
		return CommitIdentity{}, fmt.Errorf("no commit identity in %s: git config user.name is unset, "+
			"and an agent in the sandbox cannot reach the one on this machine: %w", repo, err)
	}
	email, err := git(ctx, repo, "config", "user.email")
	if err != nil {
		return CommitIdentity{}, fmt.Errorf("no commit identity in %s: git config user.email is unset, "+
			"and an agent in the sandbox cannot reach the one on this machine: %w", repo, err)
	}
	return CommitIdentity{Name: name, Email: email}, nil
}
