package node

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrGhostStore is a store that answers every read and write and keeps nothing.
var ErrGhostStore = errors.New("the log is on a filesystem that will not keep it")

// anchorName is the file Luna leaves beside the log to recognise it later.
//
// It sits next to `luna.db` rather than inside it because the check has to happen
// *before* the database is opened: opening one creates it, and a store that was
// created by the very check meant to catch it proves nothing.
const anchorName = ".luna-anchor"

// EnsureDurable refuses a log directory that will not survive the process.
//
// It exists because of a failure measured against a real `ai-jail` 1.17.0. An
// agent runs contained, with only its worktree reachable and `/` mounted tmpfs.
// Luna resolves the log to the main repository — a path the jail cannot see — so
// every level below created it fresh on the tmpfs and worked perfectly:
//
//	inside the jail:   luna task new probe   → created probe   exit=0
//	inside the jail:   luna task show probe  → events 1
//	outside the jail:  luna task show probe  → no task "probe"
//	the real store:    select * from events  → []
//
// **The command reported success and the task never existed.** Nothing was
// denied: the write went to a filesystem that evaporates, and the read that
// followed it came back from the same place, so the two agreed with each other
// and with nobody else. That is the same shape — a worktree living where the
// process cannot reach it — found in `Handover`: two realities, one answer, with
// the exit code saying 0 this time, which is worse: there is no ambiguous value
// left for a caller to distrust.
//
// The signal is deliberately not "is this tmpfs". A tmpfs log is legitimate — a
// scratch directory, a test, a CI runner with the workspace in RAM — and this
// project's own test fixtures live on one, so that check would refuse the honest
// case and still miss a bind mount that behaves the same way. What actually went
// wrong is narrower: **a log that exists outside is invisible from inside**, and
// the anchor measures exactly that.
//
// Two things are checked, because the failure has two shapes.
//
// **The anchor** catches a directory that already holds a log Luna did not write.
// **The hollow root** catches the case measured above, where the log directory does
// not exist from inside at all — so there is no anchor to be missing and nothing
// looks wrong. There the tell is the repository itself: `git` answers from the
// worktree's own `.git`, so the main repository's HEAD resolves normally, while
// its working tree is not there. A real checkout always has its own files beside
// its `.git`; a path invented by a container has only what the container made.
//
// A directory Luna has never seen, in a root that is really there, is not refused:
// a first run has to be able to create one.
func EnsureDurable(dir string) error {
	anchor := filepath.Join(dir, anchorName)

	if _, err := os.Stat(anchor); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		// Unreadable for some other reason — a permission, a broken mount. That is
		// a real error and must not be reported as a ghost: blaming containment
		// for a filesystem fault would send someone looking in the wrong place.
		return fmt.Errorf("reading the log's anchor in %s: %w", dir, err)
	}

	// No anchor: either a store from before the anchor existed, a genuine first
	// run, or a root that only exists inside this process. Only the last one is
	// refused, and the git directory is what tells it apart — it is the one place
	// visible from both sides of a sandbox, so an anchor there naming a log this
	// process cannot see is a contradiction nothing legitimate produces.
	//
	// A database with no anchor beside it is deliberately *not* the signal. It
	// looked like one — "the log was created by something that could not see the
	// real anchor" — and refused every store written before the anchor existed:
	// measured on this project's own log, which predates the guard and is as real
	// as a log gets. The contained case it meant to catch creates its database
	// and its anchor together on the same tmpfs, so the pair is always consistent
	// from inside anyway, and the git anchor is what actually catches it.
	if hollow, why := hollowRoot(dir); hollow {
		return fmt.Errorf("%w: %s", ErrGhostStore, why)
	}

	return writeAnchor(dir, anchor)
}

// hollowRoot reports whether the log's directory is one this process cannot
// really reach, which is what a contained process gets for a path outside its
// working directory.
//
// The signal is an anchor in the **git directory**, and both halves of that were
// measured rather than assumed.
//
// Two earlier signals looked right and were not. "The repository has no files
// besides .git" also describes a legitimate fresh checkout — `git init` plus an
// empty commit — and refused a real run. "The .git has no object store" fails
// too: a linked worktree needs the main repository's git directory, so the jail
// exposes it, objects and all. Measured inside ai-jail 1.17.0, `.git/objects` is
// right there.
//
// What is *not* exposed is everything else at that path. So the anchor lives in
// the git directory, where both worlds can see it, and it names the log directory
// it was written for. Outside, the anchor is there and so is the log. Inside, the
// anchor is readable and the directory it points at does not exist — which is the
// contradiction nothing else in the system reports.
func hollowRoot(dir string) (bool, string) {
	gitDir := filepath.Join(filepath.Dir(dir), ".git")
	if info, err := os.Stat(gitDir); err != nil || !info.IsDir() {
		// Not a repository, or a linked worktree's `.git` file. Neither is this
		// failure: Luna runs in plain directories too.
		return false, ""
	}

	marker := filepath.Join(gitDir, gitAnchorName)
	recorded, err := os.ReadFile(marker) //nolint:gosec // a path Luna composed
	if err != nil {
		// Never anchored here. A first run has to be able to create one, and the
		// caller does that after this returns.
		return false, ""
	}

	// Compared against the path Luna would write today rather than followed. The
	// anchor is a file in a repository, so its content is somebody else's input;
	// an anchor naming a different directory is stale or tampered with, and either
	// way it says nothing about whether *this* log is reachable.
	if strings.TrimSpace(string(recorded)) != dir {
		return false, ""
	}

	// Luna has been here and recorded this very directory. If it is not visible
	// now, this process is not seeing what Luna saw.
	if _, err := os.Stat(dir); err == nil {
		return false, ""
	}

	return true, fmt.Sprintf("%s was recorded as this repository's log directory and is not visible from here, "+
		"so this process is not seeing the real one — a contained process reaches only its working "+
		"directory, and a log written here would be reported as saved and then lost",
		dir)
}

// gitAnchorName is the marker inside `.git` that records where the log lives.
//
// In the git directory rather than beside the log because that is the one place
// reachable from both sides of a sandbox: a linked worktree cannot function
// without the main repository's git directory, so the jail exposes it.
const gitAnchorName = "luna-log-location"

// writeAnchor records that Luna has seen this directory.
//
// Two files, because they answer different questions. The one beside the log says
// "Luna wrote this store"; the one in the git directory says "the log is over
// there" — and only the second is legible to a contained process.
func writeAnchor(dir, anchor string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	const note = "Luna wrote this to recognise its own log directory. Do not delete it:\n" +
		"without it, a contained process cannot tell this log from one it invented.\n"
	if err := os.WriteFile(anchor, []byte(note), 0o600); err != nil {
		return fmt.Errorf("writing the log's anchor in %s: %w", dir, err)
	}

	// Best effort: a repository Luna cannot write to still gets a working log, and
	// the guard simply has nothing to compare against next time.
	gitDir := filepath.Join(filepath.Dir(dir), ".git")
	if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
		_ = os.WriteFile(filepath.Join(gitDir, gitAnchorName), []byte(dir+"\n"), 0o600)
	}
	return nil
}
