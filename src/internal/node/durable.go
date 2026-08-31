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

// EnsureDurable refuses a log directory that will not survive the process.
//
// It exists because of a failure measured against a real `ai-jail` 1.17.0. An
// agent runs contained, with only its worktree reachable and `/` mounted tmpfs.
// Luna resolves the log to the central data home — a path the jail cannot see —
// so every level below created it fresh on the tmpfs and worked perfectly:
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
// One thing is checked, and it is the git directory. There the tell is the
// repository itself: `git` answers from the worktree's own `.git`, so the main
// repository's HEAD resolves normally while its working tree is not there. A real
// checkout always has its own files beside its `.git`; a path invented by a
// container has only what the container made.
//
// A directory Luna has never seen, in a root that is really there, is not refused:
// a first run has to be able to create one.
//
// There used to be a second marker, a file beside the log saying "Luna wrote this
// store". It was only ever a shortcut — present, accept and skip the git check —
// and it caught nothing on its own: the code below is what detects the failure,
// and the contained case creates its database and its marker together on the same
// tmpfs, so the pair is always consistent from inside anyway. Removing it costs
// one stat and one read per command, and takes a file out of every project that
// runs Luna.
func EnsureDurable(repo, dir string) error {
	if hollow, why := hollowRoot(repo, dir); hollow {
		return fmt.Errorf("%w: %s", ErrGhostStore, why)
	}

	return recordLogLocation(repo, dir)
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
//
// The repository is passed in rather than derived from the log directory, and
// that is what kept this working when the log left the checkout. It used to take
// the log's parent as the repository, which was true only while the log lived
// inside one; with the log under `$XDG_DATA_HOME` that lookup finds no `.git`,
// returns "nothing to compare", and the guard goes quietly inert. A guard that
// stops guarding without saying so is worse than one that was never written.
//
// The move also made it *more* sensitive, not less: ai-jail gives a contained
// process a tmpfs `$HOME`, so a log under it is not merely unreachable — the whole
// path is absent, which is exactly what this stats for.
func hollowRoot(repo, dir string) (bool, string) {
	gitDir := filepath.Join(repo, ".git")
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

	return true, fmt.Sprintf("%s was recorded as the central log directory and is not visible from here, "+
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

// recordLogLocation writes down where the log lives, in the one place a contained
// process can also read.
func recordLogLocation(repo, dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	// Best effort: a repository Luna cannot write to still gets a working log, and
	// the guard simply has nothing to compare against next time.
	gitDir := filepath.Join(repo, ".git")
	if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
		_ = os.WriteFile(filepath.Join(gitDir, gitAnchorName), []byte(dir+"\n"), 0o600)
	}
	return nil
}
