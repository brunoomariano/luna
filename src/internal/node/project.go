package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Project is what a task belongs to, and it is not a checkout.
//
// Two clones of one repository are one project: a worktree opened to review a
// task has to see that task, and a second clone made to work on a branch is the
// same work. The remote is what says so — it is the only thing both clones agree
// on and neither can change by moving.
type Project struct {
	// Key is the stable project scope stored with every event and artifact.
	Key string

	// From is what the key was derived from, kept so a global task listing can
	// explain why two checkouts share a scope — or why two expected ones do not.
	From string

	// Remote reports whether the key came from a remote. A repository with none
	// is keyed by its path, and that is a different guarantee: it is one project
	// per checkout, because there is nothing else to tie two together.
	Remote bool
}

// scpLike matches `git@host:owner/repo`, which is not a URL and does not parse as
// one. It is the default form for github and gitlab, so it is the common case
// rather than an edge.
var scpLike = regexp.MustCompile(`^[\w.+-]+@([\w.-]+):(.+)$`)

// unsafeInKey is everything a directory name should not carry. Replaced rather
// than stripped, so two URLs differing only in punctuation cannot collapse onto
// one key.
var unsafeInKey = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// IdentifyProject says which project a directory belongs to.
//
// A repository with no remote is keyed by its path, and that is stated rather
// than hidden: it means two clones of it are two projects, because nothing ties
// them together. Adding a remote later changes the key, and so changes which log
// the checkout reads — which is correct and worth knowing, since before the
// remote there was no shared project to read.
func IdentifyProject(ctx context.Context, dir string) (Project, error) {
	root, err := Root(ctx, dir)
	if err != nil {
		return Project{}, err
	}

	url, err := git(ctx, dir, "remote", "get-url", "origin")
	if err != nil || strings.TrimSpace(url) == "" {
		return Project{Key: pathKey(root), From: root}, nil
	}

	normal := NormalizeRemote(strings.TrimSpace(url))
	return Project{Key: keyOf(normal, normal), From: normal, Remote: true}, nil
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
// What is *not* removed is the host. `github.com/me/app` and
// `gitlab.com/me/app` are two projects that happen to share a name, and merging
// them would put one project's tasks in another's log.
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

// keyOf turns something identifying into a compact, readable database key.
//
// Readable first, unique always, and the two arguments are why: `readable` is
// what lets somebody recognise their own work in a report, and `identity` is
// what the hash is taken over. They are separate because the
// readable half is lossy — truncated, lowercased, stripped of punctuation — and
// hashing the lossy form would let two different projects land on one key.
func keyOf(readable, identity string) string {
	safe := strings.Trim(unsafeInKey.ReplaceAllString(readable, "-"), "-")
	if len(safe) > 48 {
		safe = safe[:48]
	}
	if safe == "" {
		safe = "project"
	}
	return safe + "-" + digest(identity)
}

// pathKey is the fallback for a repository with no remote.
//
// The whole path is the identity and the base name is only the readable half.
// Hashing the base name alone was the first attempt and was wrong: two checkouts
// of different projects both called `app` would have shared a log.
func pathKey(root string) string {
	return keyOf(filepath.Base(root), root)
}

// digest is the short hash both key forms end with.
//
// Eight hex characters: this separates paths a person wrote, not adversarial
// collisions, and a short value is one somebody can compare by eye in a listing.
func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}

// String is the project as a person would name it.
func (p Project) String() string {
	if p.Remote {
		return p.From
	}
	return fmt.Sprintf("%s (no remote)", p.From)
}
