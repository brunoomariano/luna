package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNoClaudeConfig is a claude that has never run for this user.
var ErrNoClaudeConfig = errors.New("no claude configuration to add trust to")

// TrustWorktreeParent records the directory Luna's worktrees are created in as
// trusted in the user's claude configuration, and returns that directory.
//
// It exists because of what happens without it: inside the sandbox, claude opens
// its folder-trust dialog for every worktree — each one is a path the
// configuration has never seen — and an agent sitting at a dialog is a stage
// that closes having delivered nothing. The Enter nudge (see the herdr runner)
// answers the dialog when it appears; this removes the reason it appears, once,
// by an explicit act of the person whose configuration it is.
//
// Three rules, each learned the hard way:
//
//   - **Merge, never replace.** The first attempt at trust wrote the file whole
//     and was measured destroying the real one through a sandbox's read-write
//     bind — OAuth included. Here the file is read, one key is added, and
//     everything else survives byte for byte.
//   - **Never create the file.** A missing configuration means claude has never
//     run; a file Luna invents would be replaced by claude's own first start,
//     and trust recorded in it would be lost anyway. The person runs claude
//     once first.
//   - **Atomic.** The write goes to a sibling temp file and renames over, so a
//     crash mid-write cannot leave a half-written configuration — this file
//     holds the user's credentials, and "mostly valid JSON" is not valid JSON.
func TrustWorktreeParent(home, repo string) (string, error) {
	parent, err := worktreeParent(repo)
	if err != nil {
		return "", err
	}

	configPath := filepath.Join(home, ".claude.json")
	raw, err := os.ReadFile(configPath) //nolint:gosec // the user's own home
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%w: %s does not exist — run claude once so it creates its own, then try again",
			ErrNoClaudeConfig, configPath)
	}
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", configPath, err)
	}

	out, changed, err := withTrustedParent(raw, parent, configPath)
	if err != nil {
		return "", err
	}
	if !changed {
		return parent, nil // already trusted; nothing to write is the right write
	}
	return parent, replaceFile(configPath, out)
}

// withTrustedParent adds the one trust key to a configuration's bytes, touching
// nothing else, and reports whether anything actually changed.
func withTrustedParent(raw []byte, parent, configPath string) (out []byte, changed bool, err error) {
	config := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, false, fmt.Errorf("%s does not parse, and a file that holds credentials is not one to fix by guessing: %w",
			configPath, err)
	}

	projects := map[string]map[string]any{}
	if rawProjects, ok := config["projects"]; ok {
		if err := json.Unmarshal(rawProjects, &projects); err != nil {
			return nil, false, fmt.Errorf("the projects in %s do not parse: %w", configPath, err)
		}
	}

	if entry, ok := projects[parent]; ok && entry["hasTrustDialogAccepted"] == true {
		return nil, false, nil
	}
	if projects[parent] == nil {
		projects[parent] = map[string]any{}
	}
	projects[parent]["hasTrustDialogAccepted"] = true

	merged, err := json.Marshal(projects)
	if err != nil {
		return nil, false, fmt.Errorf("rebuilding the projects for %s: %w", configPath, err)
	}
	config["projects"] = merged

	out, err = json.Marshal(config)
	if err != nil {
		return nil, false, fmt.Errorf("rebuilding %s: %w", configPath, err)
	}
	return out, true, nil
}

// worktreeParent is the directory a repository's worktrees are created in: its
// own parent, per checkoutPath's sibling rule.
func worktreeParent(repo string) (string, error) {
	absolute, err := filepath.Abs(repo)
	if err != nil {
		return "", fmt.Errorf("resolving the repository path %q: %w", repo, err)
	}
	parent := filepath.Dir(absolute)
	if parent == string(filepath.Separator) {
		return "", fmt.Errorf("the repository %q sits at the filesystem root, and trusting the root is not a scope, "+
			"it is the absence of one", absolute)
	}
	return parent, nil
}

// replaceFile writes content beside path and renames it over, keeping the
// original's permissions.
func replaceFile(path string, content []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("checking %s before replacing it: %w", path, err)
	}

	temp := path + ".luna-tmp"
	if err := os.WriteFile(temp, content, info.Mode().Perm()); err != nil {
		return fmt.Errorf("staging the new %s: %w", path, err)
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}
