package node

import (
	"context"
	"strings"
	"testing"
)

// TestTheIdentityComesFromTheRepository is the happy path: a repository that
// knows who it commits as hands that over to the stage.
func TestTheIdentityComesFromTheRepository(t *testing.T) {
	dir := repo(t)

	identity, err := ReadCommitIdentity(context.Background(), dir)
	if err != nil {
		t.Fatalf("reading the identity of a configured repository: %v", err)
	}
	if identity.Name != "Test" || identity.Email != "test@example.invalid" {
		t.Errorf("want Test <test@example.invalid>, got %s <%s>", identity.Name, identity.Email)
	}
}

// TestTheIdentityTravelsAsEnvironment pins how the identity crosses into the
// sandbox.
//
// Not as a config file: on a machine where `~/.gitconfig` is a symlink into a
// dotfiles repository, mapping the path into the jail resolves to somewhere the
// jail cannot see, so the identity is still absent. The four variables git reads
// before any config file are the only channel that works from outside.
func TestTheIdentityTravelsAsEnvironment(t *testing.T) {
	env := CommitIdentity{Name: "Test", Email: "test@example.invalid"}.Env()

	// The committer as well as the author. Setting only the author sends git
	// looking for the config that is not there, which fails the commit for the
	// same reason with a different message.
	for _, want := range []string{
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@example.invalid",
	} {
		if !contains(env, want) {
			t.Errorf("the identity does not carry %q, so a commit in the sandbox fails on it: %v", want, env)
		}
	}
}

// TestARepositoryWithNoIdentityIsRefused keeps a stage from being paid for
// before it can possibly deliver.
//
// An agent whose git cannot name a committer does the work and then cannot
// commit it. Discovering that at the handover means the model's budget is
// already spent; discovering it here costs one `git config`.
func TestARepositoryWithNoIdentityIsRefused(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "git", "init", "--initial-branch=main")

	// The machine running this may well have a global identity, and the point is
	// a repository that resolves to none. Pointing git's global and system config
	// at nothing is what makes the test about the repository rather than about
	// whoever runs it.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")

	_, err := ReadCommitIdentity(context.Background(), dir)
	if err == nil {
		t.Fatal("a repository with no identity must be refused before the agent runs")
	}
	if !strings.Contains(err.Error(), "commit identity") {
		t.Errorf("the refusal must say what is missing, got: %v", err)
	}
}

func contains(list []string, want string) bool {
	for _, got := range list {
		if got == want {
			return true
		}
	}
	return false
}
