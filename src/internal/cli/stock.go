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

// LoadRoles reads the role definitions from a stock directory.
//
// One file per role, named after it — `reviewer.toml` defines `reviewer`. The
// name comes from the filename rather than a field so it cannot disagree with
// itself, which is the mistake a `[role.reviewer]` header inside `scout.toml`
// invites (RFC-0003).
//
// The keys are exactly the ones `[role.*]` accepts in a project's config, and
// they are parsed by the same code. A stock file and an override are the same
// format, so learning one teaches the other.
func LoadRoles(files fs.FS, dir string) (map[fsm.RoleName]fsm.Role, error) {
	names, err := tomlFiles(files, dir)
	if err != nil {
		return nil, err
	}

	roles := map[fsm.RoleName]fsm.Role{}
	cfg := Config{Roles: roles, Profiles: map[fsm.Profile]Policy{}}

	for _, file := range names {
		name := strings.TrimSuffix(file, ".toml")

		content, err := fs.ReadFile(files, path.Join(dir, file))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", file, err)
		}
		if err := eachSetting(string(content), file, func(key, value, at string) error {
			return assignRole(&cfg, name, key, value, at)
		}); err != nil {
			return nil, err
		}
	}

	if len(roles) == 0 {
		return nil, fmt.Errorf("no role files in %s: a flow names roles, and none would resolve", dir)
	}
	return roles, nil
}

// LoadProfiles reads the profile definitions from a stock directory.
//
// The same shape as roles: one file per profile, named after it.
func LoadProfiles(files fs.FS, dir string) (map[fsm.Profile]Policy, error) {
	names, err := tomlFiles(files, dir)
	if err != nil {
		return nil, err
	}

	profiles := map[fsm.Profile]Policy{}
	cfg := Config{Roles: map[fsm.RoleName]fsm.Role{}, Profiles: profiles}

	for _, file := range names {
		name := strings.TrimSuffix(file, ".toml")

		content, err := fs.ReadFile(files, path.Join(dir, file))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", file, err)
		}

		// Declared before its settings are read, so a profile whose file states no
		// budget carries the shipped one rather than being a name that never
		// appears.
		profiles[fsm.Profile(name)] = Policy{Budgets: fsm.DefaultBudgets()}

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
				"and %q belongs in a project's config.toml", at, line)
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

// shippedRoles and shippedProfiles parse the embedded stock once.
//
// Cached for the same reason the flow is: they are read on every command, and
// re-parsing a dozen files each time would put a cost on something that never
// changes within a process.
var (
	shippedRoles = sync.OnceValues(func() (map[fsm.RoleName]fsm.Role, error) {
		return LoadRoles(stock.Files, stock.RolesDir)
	})

	shippedProfiles = sync.OnceValues(func() (map[fsm.Profile]Policy, error) {
		return LoadProfiles(stock.Files, stock.ProfilesDir)
	})
)
