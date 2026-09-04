package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/brunoomariano/luna/src/internal/skills"
)

// harnesses is where each agent looks for skills.
//
// A closed set, because the wrong guess writes a directory tree into somebody's
// home that nothing will ever read. A harness that is not here is reachable with
// --dir, which is honest about being the caller's decision.
var harnesses = map[string]string{
	"claude": filepath.Join(".claude", "skills"),
	"codex":  filepath.Join(".codex", "skills"),
}

func installSkillsCommand(env Env, args []string) error {
	set := flags("install-skills", env)
	var (
		dir     = set.String("dir", "", "install into this directory instead of a known harness's")
		printIt = set.Bool("print", false, "print the skill and exit, installing nothing")
		dryRun  = set.Bool("dry-run", false, "say what would be written, write nothing")
	)

	harness, rest := positional(args)
	if err := set.Parse(rest); err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}

	carried := skills.All()
	if *printIt {
		return printSkills(env, carried)
	}

	target, err := skillsDir(harness, *dir)
	if err != nil {
		return err
	}
	return installSkills(env, carried, target, *dryRun)
}

// skillsDir resolves where the skills go, and refuses rather than guessing.
func skillsDir(harness, override string) (string, error) {
	if override != "" {
		if harness != "" {
			return "", fmt.Errorf("%w: --dir and %q both name a destination — pass one", ErrUsage, harness)
		}
		return override, nil
	}
	if harness == "" {
		return "", fmt.Errorf("%w: install-skills needs a harness — %s — or --dir",
			ErrUsage, strings.Join(knownHarnesses(), " or "))
	}

	under, known := harnesses[harness]
	if !known {
		return "", fmt.Errorf("%w: %q is not a harness Luna knows (%s). If it reads skills from "+
			"somewhere else, name it with --dir", ErrUsage, harness, strings.Join(knownHarnesses(), ", "))
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot tell where your home directory is, so %q cannot be resolved — "+
			"pass --dir: %w", harness, err)
	}
	return filepath.Join(home, under), nil
}

func knownHarnesses() []string {
	names := make([]string, 0, len(harnesses))
	for name := range harnesses {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// installSkills writes each skill into its own directory under target.
//
// Overwriting is the whole idea: running this twice leaves what running it once
// did, and an install that refuses because a file is already there is an install
// nobody runs the second time. What it will not do is delete — a directory it
// did not write is not its business.
func installSkills(env Env, carried []skills.Skill, target string, dryRun bool) error {
	for _, skill := range carried {
		root := filepath.Join(target, skill.Name)
		for _, name := range sortedNames(skill.Files) {
			path := filepath.Join(root, name)
			if dryRun {
				fmt.Fprintf(env.Out, "would write %s\n", path)
				continue
			}
			if err := writeSkillFile(path, skill.Files[name]); err != nil {
				return err
			}
			fmt.Fprintf(env.Out, "wrote %s\n", path)
		}
	}
	if dryRun {
		fmt.Fprintln(env.Out, "\n(--dry-run: nothing was written)")
		return nil
	}

	fmt.Fprintf(env.Out, "\nA new session in that harness can now be told to use the "+
		"`%s` skill.\n", carried[0].Name)
	return nil
}

func writeSkillFile(path string, body []byte) error {
	// 0700/0600: a skill lives under somebody's home and is read by the agent
	// running as them. Nothing else needs it — the same reasoning, and the same
	// numbers, as the ledger's own file.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("making room for %s: %w", path, err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// printSkills writes the skills to stdout for somebody who would rather read
// them, or place them, themselves.
func printSkills(env Env, carried []skills.Skill) error {
	for _, skill := range carried {
		for _, name := range sortedNames(skill.Files) {
			if len(carried) > 1 || len(skill.Files) > 1 {
				fmt.Fprintf(env.Out, "===== %s/%s =====\n", skill.Name, name)
			}
			if _, err := env.Out.Write(skill.Files[name]); err != nil {
				return fmt.Errorf("printing %s: %w", name, err)
			}
		}
	}
	return nil
}

func sortedNames(files map[string][]byte) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
