package cli

import (
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/stock"
)

// LoadProfiles reads the profile definitions from a stock directory.
//
// One file per profile, named after it — `nightly.toml` defines `nightly`. The
// name comes from the filename rather than a field so it cannot disagree with
// itself.
func LoadProfiles(files fs.FS, dir string) (map[fsm.Profile]bool, error) {
	names, err := tomlFiles(files, dir)
	if err != nil {
		return nil, err
	}

	profiles := map[fsm.Profile]bool{}
	cfg := Config{Profiles: profiles}

	for _, file := range names {
		name := strings.TrimSuffix(file, ".toml")

		content, err := fs.ReadFile(files, path.Join(dir, file))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", file, err)
		}

		// Declared before its settings are read, so a profile whose file states no
		// budget carries the shipped one rather than being a name that never
		// appears.
		profiles[fsm.Profile(name)] = true

		if err := eachSetting(string(content), file, func(key, value, at string) error {
			return assignProfile(&cfg, name, key, value, at)
		}); err != nil {
			return nil, err
		}
	}

	if len(profiles) == 0 {
		return nil, fmt.Errorf("no profile files in %s: every task carries a profile", dir)
	}
	return profiles, nil
}

// assignProfile refuses every key inside a profile file.
//
// A profile holds only its name. What it used to carry moved to where each thing
// is actually decided: whether a gate waits is the stage's declaration, who
// answers it is the knob, and the watchdog's clock is project-wide. What survives
// is the name — a task's log carries the one it was created under, and `task new
// --profile` validates against the set.
//
// Refused rather than ignored. A file that loads and decides nothing is the
// silent kind of wrong: somebody keeps a file that reads like supervision and
// gets none.
func assignProfile(_ *Config, section, key, _, where string) error {
	return fmt.Errorf("%s: unknown setting %q in profile %s — a profile holds no "+
		"settings now, only its name", where, key, section)
}

// stripComment drops a trailing `#` comment, leaving one inside quotes alone —
// a value may legitimately contain a hash.
func stripComment(line string) string {
	quoted := false
	for i, r := range line {
		switch {
		case r == '"':
			quoted = !quoted
		case r == '#' && !quoted:
			return line[:i]
		}
	}
	return line
}

// eachSetting walks a flat `key = value` file, calling assign for each line.
//
// Flat because the name is the filename: a stock file has no sections, so a
// `[header]` in one is a file written against the wrong format and is refused
// rather than ignored.
func eachSetting(content, where string, assign func(key, value, at string) error) error {
	for number, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(stripComment(raw))
		if line == "" {
			continue
		}
		at := fmt.Sprintf("%s:%d", where, number+1)

		if strings.HasPrefix(line, "[") {
			return fmt.Errorf("%s: a stock file has no sections — its name is the filename, "+
				"and %q is a header it has no place for", at, line)
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			return fmt.Errorf("%s: expected key = value, got %q", at, line)
		}
		if err := assign(strings.TrimSpace(key), strings.TrimSpace(value), at); err != nil {
			return err
		}
	}
	return nil
}

// tomlFiles lists the .toml files in a directory, sorted.
func tomlFiles(files fs.FS, dir string) ([]string, error) {
	entries, err := fs.ReadDir(files, dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		names = append(names, e.Name())
	}
	return names, nil
}

// shippedProfiles parses the embedded stock once.
//
// Cached for the same reason the flow is: it is read on every command, and
// re-parsing the files each time would put a cost on something that never
// changes within a process.
var shippedProfiles = sync.OnceValues(func() (map[fsm.Profile]bool, error) {
	return LoadProfiles(stock.Files, stock.ProfilesDir)
})
