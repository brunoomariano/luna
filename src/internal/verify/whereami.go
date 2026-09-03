package verify

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
)

// Where is what a checkout says about itself.
//
// Everything here is read from git and the path. Nothing is read from a file
// Luna wrote, because a marker file would be a second thing to keep in step with
// the first — and because Luna writes nothing into a checkout at all.
type Where struct {
	// Repo is the main repository's root, which outlives every worktree cut from
	// it and is where a delivery is verified.
	Repo string

	// Project names the repository the way a report should group by: the
	// normalised remote, or the path when there is none.
	Project string

	// Worktree is the checkout this was asked from.
	Worktree string

	// Branch is what is checked out.
	Branch string

	// Run is the run this checkout belongs to, when the branch names one.
	Run string

	// Phase is the phase, when the branch names one.
	Phase string
}

// lunaBranch matches the branch a conductor is asked to use: `luna/<run>/<phase>`.
//
// The branch is the authority rather than the directory name, because a branch
// travels with the work and a directory can be moved or made by hand.
var lunaBranch = regexp.MustCompile(`^luna/([^/]+)/([^/]+)$`)

// scpLike matches `git@host:owner/repo`, which is not a URL and does not parse as
// one. It is the default form for github and gitlab, so it is the common case
// rather than an edge.
var scpLike = regexp.MustCompile(`^[\w.+-]+@([\w.-]+):(.+)$`)

// Identify answers what a directory already is.
//
// Nothing here fails for being outside a repository: Luna is a tool somebody may
// run anywhere, and refusing to say where you are because you are nowhere in
// particular helps no one.
func Identify(ctx context.Context, dir string) Where {
	where := Where{Worktree: absolute(dir), Repo: absolute(dir)}

	if common, err := git(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
		// --git-common-dir points at the `.git` of the main repository; the root is
		// its parent. That is what makes this the repository rather than the
		// worktree, which is the distinction every check here depends on.
		where.Repo = filepath.Dir(common)
	}

	where.Project = where.Repo
	if url, err := git(ctx, dir, "remote", "get-url", "origin"); err == nil && strings.TrimSpace(url) != "" {
		where.Project = NormalizeRemote(url)
	}

	branch, err := git(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return where
	}
	where.Branch = branch
	if m := lunaBranch.FindStringSubmatch(branch); m != nil {
		where.Run, where.Phase = m[1], m[2]
	}
	return where
}

// NormalizeRemote reduces the ways of naming one repository to a single string.
//
// The forms that have to land together are the ones a person actually has:
// `git@host:owner/repo.git` from a default clone, `https://host/owner/repo` from
// the browser, and `ssh://git@host/owner/repo.git` from a config that spells it
// out. They differ in scheme, in credentials, in the `.git` suffix and in a
// trailing slash, and every one of those differences is punctuation rather than
// identity.
//
// What is *not* removed is the host. `github.com/me/app` and `gitlab.com/me/app`
// are two projects that happen to share a name, and merging them would put one
// project's runs under another's name.
func NormalizeRemote(url string) string {
	url = strings.TrimSpace(url)

	if m := scpLike.FindStringSubmatch(url); m != nil {
		url = m[1] + "/" + m[2]
	} else {
		// The scheme and any credentials in front of the host. Cut on "://" rather
		// than parsing, because the scp-like form above is what a URL parser gets
		// wrong, and it is already handled.
		if _, rest, found := strings.Cut(url, "://"); found {
			url = rest
		}
		if _, rest, found := strings.Cut(url, "@"); found {
			url = rest
		}
	}

	url = strings.TrimSuffix(url, "/")
	url = strings.TrimSuffix(url, ".git")
	// Case is not identity on a host, and forge paths are case-insensitive in
	// practice: `github.com/Me/App` and `github.com/me/app` are one repository.
	return strings.ToLower(url)
}

func absolute(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return abs
}
