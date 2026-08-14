package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/brunoomariano/luna/src/stock"
)

// StockDir is where a project's copy of the stock lives, beside its config and
// its log.
func StockDir(storePath string) string {
	return filepath.Join(filepath.Dir(storePath), "stock")
}

// initCommand writes the shipped stock into the project.
//
// This is what turns ADR-0017's replaceable flow into something a person can
// actually replace: the stages, roles and profiles Luna ships with, as files to
// edit (RFC-0003). Until a project runs it, the embedded copy is what runs — so
// a repository that never wants to customise anything never has to.
//
// The copy is complete rather than partial. A directory holding three of the
// fourteen stages raises "does this replace the default or extend it?", and
// whichever answer is chosen, someone reads the other one into it.
func initCommand(env Env, args []string) error {
	force, err := parseInitOptions(args)
	if err != nil {
		return err
	}

	if env.Stock == "" {
		return fmt.Errorf("no project directory to write into")
	}

	// Refusing by default is the whole safety of this command: the files it
	// writes are ones a person edits, and overwriting them silently would throw
	// away exactly the customisation the command exists to enable.
	if !force {
		if entries, err := os.ReadDir(env.Stock); err == nil && len(entries) > 0 {
			return fmt.Errorf("%s already has a stock — edit it, or `luna init --force` "+
				"to replace it with the shipped one (which discards your edits)", env.Stock)
		}
	}

	// Replace means replace. Overwriting file by file leaves behind anything the
	// shipped flow no longer carries — and a leftover stage is a valid stage, so
	// the loader keeps running it and nothing says why. Measured after ADR-0062
	// removed two stages: `luna flow check` still reported fourteen.
	if force {
		if err := clearStock(env.Stock); err != nil {
			return err
		}
	}

	written, err := copyStock(env.Stock)
	if err != nil {
		return err
	}

	fmt.Fprintf(env.Out, "wrote %d files to %s\n", written, env.Stock)
	fmt.Fprintf(env.Out, "\nthis project now runs its own flow. `luna flow check` says whether "+
		"it still holds together,\nand whether any open task was written under a different one\n")
	return nil
}

// copyStock writes every embedded file into dir, and reports how many.
func copyStock(dir string) (int, error) {
	var written int

	err := fs.WalkDir(stock.Files, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(dir, name), 0o750)
		}

		content, err := fs.ReadFile(stock.Files, name)
		if err != nil {
			return fmt.Errorf("reading the shipped %s: %w", name, err)
		}
		// 0o600: these are files a person edits, and nothing else needs them.
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
		written++
		return nil
	})
	if err != nil {
		return 0, err
	}
	return written, nil
}

// ProjectStock opens a project's stock, or reports that there is none.
//
// A project without one runs the embedded copy, which is the ordinary case and
// not a failure: `luna init` is opt-in, and a repository that never customises
// anything never needs it.
func ProjectStock(dir string) (fs.FS, bool) {
	if dir == "" {
		return nil, false
	}
	// The stages are what make a directory a stock. Roles and profiles have
	// working defaults; a flow does not.
	if _, err := os.Stat(filepath.Join(dir, stock.StagesDir)); err != nil {
		return nil, false
	}
	return os.DirFS(dir), true
}

// describeStock says which stock is in use, for `luna flow check`.
func describeStock(dir string) string {
	if _, ok := ProjectStock(dir); ok {
		return dir
	}
	return "the shipped stock (run `luna init` to copy it here and edit it)"
}

// stockNote is the line `luna flow check` prints about where the flow came from.
func stockNote(dir string) string {
	return strings.TrimSpace("from " + describeStock(dir))
}

// clearStock empties the directories the stock owns, leaving anything else alone.
//
// Named subdirectories rather than the whole tree: `.luna` holds the log and
// whatever else a project keeps there, and `--force` is about the stock.
func clearStock(dir string) error {
	for _, sub := range []string{"stages", "roles", "profiles"} {
		path := filepath.Join(dir, sub)
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("clearing %s: %w", path, err)
		}
	}
	return nil
}

// parseInitOptions reads the one flag this command takes.
func parseInitOptions(args []string) (force bool, err error) {
	flags, err := parseFlags(args)
	if err != nil {
		return false, err
	}
	for name := range flags {
		if name != "force" {
			return false, fmt.Errorf("%w: unknown flag --%s", ErrUsage, name)
		}
	}
	_, force = flags["force"]
	return force, nil
}
