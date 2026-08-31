package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/brunoomariano/luna/src/internal/store"
)

// configCommand shows or changes what a project and the machine are configured
// with.
//
// The settings live in the one database the daemon owns, keyed by project, and
// this is the only surface that writes them. They used to live in
// `.luna/config.toml`, which is why the shape here is a key and a value rather
// than a file: what changed is where they are kept, not what they are. That file
// is not read at all any more — not even once, to import it.
func configCommand(env Env, args []string) error {
	if len(args) == 0 {
		return showConfig(env)
	}
	switch args[0] {
	case "set":
		return setConfig(env, args[1:])
	case "unset":
		return unsetConfig(env, args[1:])
	default:
		return fmt.Errorf("%w: unknown config subcommand %q (expected set, unset, or nothing)",
			ErrUsage, args[0])
	}
}

// showConfig prints both scopes and says which value is in effect.
//
// Both, rather than only the result: a person surprised by an editor wants to
// know whether this project chose it or the machine did, and a merged view
// answers "what am I running under" while leaving "why" unanswerable.
func showConfig(env Env) error {
	global, project, err := readScopes(env)
	if err != nil {
		return err
	}

	fmt.Fprintf(env.Out, "project %s\n\n", env.Store.Project)
	for _, key := range ConfigKeys() {
		value, from := inEffect(key, global, project, env.Store.Project)
		if value == "" {
			fmt.Fprintf(env.Out, "  %-13s %s\n", key, "(unset)")
			continue
		}
		fmt.Fprintf(env.Out, "  %-13s %-28s %s\n", key, value, from)
	}

	for _, key := range DiscoveredKeys() {
		value := project[key]
		if value == "" {
			fmt.Fprintf(env.Out, "  %-13s %s\n", key, "(nothing found yet)")
			continue
		}
		fmt.Fprintf(env.Out, "  %-13s %-28s discovered by setup\n", key, value)
	}

	if overridden := shadowed(global, project); len(overridden) > 0 {
		fmt.Fprintf(env.Out, "\nthe machine also sets %s, which this project overrides\n",
			strings.Join(overridden, ", "))
	}
	fmt.Fprintf(env.Out, "\n  luna config set <key> <value>\n  luna config unset <key>\n")
	return nil
}

// inEffect is the value a command would run under, and where it came from.
func inEffect(key string, global, project map[string]string, name string) (value, from string) {
	if value, ok := project[key]; ok {
		return value, "project " + name
	}
	if value, ok := global[key]; ok {
		return value, "this machine"
	}
	return "", ""
}

// shadowed names the machine settings this project has taken over, so the
// listing does not silently drop a value somebody set.
func shadowed(global, project map[string]string) []string {
	var names []string
	for key := range global {
		if _, taken := project[key]; taken {
			names = append(names, key)
		}
	}
	sort.Strings(names)
	return names
}

func setConfig(env Env, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("%w: set needs a key and a value", ErrUsage)
	}
	key, value := args[0], strings.Join(args[1:], " ")
	if err := CheckConfigKey(key); err != nil {
		return err
	}

	// Parsed before it is stored, by the parser that reads it back. A turn budget
	// of "fortnight" written into the database is a setting that looks applied and
	// fails at the next run, in a place that says nothing about where it came from.
	if _, err := ConfigFrom(nil, map[string]string{key: value}); err != nil {
		return err
	}

	scope := ScopeOf(key, env.Store.Project)
	if err := env.Store.PutSetting(scope, key, value); err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "%s = %s (%s)\n", key, value, scopeName(scope, env.Store.Project))
	return nil
}

func unsetConfig(env Env, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: unset needs one key", ErrUsage)
	}
	key := args[0]
	if err := CheckConfigKey(key); err != nil {
		return err
	}

	scope := ScopeOf(key, env.Store.Project)
	if err := env.Store.PutSetting(scope, key, ""); err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "%s unset (%s)\n", key, scopeName(scope, env.Store.Project))
	return nil
}

func readScopes(env Env) (global, project map[string]string, err error) {
	if global, err = env.Store.Settings(store.GlobalScope); err != nil {
		return nil, nil, err
	}
	if project, err = env.Store.Settings(env.Store.Project); err != nil {
		return nil, nil, err
	}
	return global, project, nil
}

func scopeName(scope, project string) string {
	if scope == store.GlobalScope {
		return "this machine"
	}
	return "project " + project
}
