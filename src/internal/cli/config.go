package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is what a project can set for itself, read from `.luna/config.toml`.
//
// It exists because a person's `$EDITOR` serves their git, not necessarily this
// project: a repository whose contracts are long markdown may want a different
// editor than the one that opens commit messages. Everything here is optional —
// a project with no config file behaves exactly as before.
type Config struct {
	// Editor overrides $EDITOR for `luna gate adjust`. It may carry arguments,
	// like `code --wait`.
	Editor string
}

// LoadConfig reads the project's configuration.
//
// A missing file is not an error: it is the ordinary case, and returning a zero
// Config keeps every caller from having to distinguish "no file" from "empty
// file".
func LoadConfig(path string) (Config, error) {
	content, err := os.ReadFile(path) //nolint:gosec // the path comes from the CLI, not from input
	if os.IsNotExist(err) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("reading %s: %w", path, err)
	}

	return parseConfig(string(content), path)
}

// parseConfig reads the subset of TOML this file needs: `key = "value"`, plus
// comments and blank lines.
//
// A hand-rolled parser rather than a dependency, because the config has one key
// and a TOML library would be the project's second external dependency for
// something a dozen lines cover. If the file ever grows sections or arrays, that
// trade flips and this should be replaced rather than extended.
func parseConfig(content, path string) (Config, error) {
	var cfg Config

	for number, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			return Config{}, fmt.Errorf("%s:%d: expected key = value, got %q", path, number+1, line)
		}

		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"`)

		switch key {
		case "editor":
			cfg.Editor = value
		default:
			// An unknown key is an error rather than a warning: a typo in
			// `editor` would otherwise leave the setting silently unapplied, and
			// the person would conclude the feature does not work.
			return Config{}, fmt.Errorf("%s:%d: unknown setting %q", path, number+1, key)
		}
	}

	return cfg, nil
}

// ConfigPath is where a project's configuration lives, next to its store.
func ConfigPath(storePath string) string {
	return filepath.Join(filepath.Dir(storePath), "config.toml")
}
