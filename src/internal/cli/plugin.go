package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// pluginID is what herdr registers the proxy under.
const pluginID = "luna.chat"

// pluginCommand installs the herdr proxy, or says why it cannot.
//
// It exists because the install is three commands and four things that can be
// wrong, and every one of them fails with a message about herdr rather than
// about Luna: no herdr on PATH, no manifest where it was expected, no `luna` for
// the pane to run. A person meeting any of those reads it as a broken plugin.
//
// What it does not do is hide the commands. It prints what it ran, so someone
// who wants to undo it or do it by hand knows exactly what happened.
func pluginCommand(env Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: plugin needs a subcommand (install, uninstall, status)", ErrUsage)
	}

	switch args[0] {
	case "install":
		return pluginInstall(env, args[1:])
	case "uninstall":
		return pluginUninstall(env, args[1:])
	case "status":
		return pluginStatus(env, args[1:])
	default:
		return fmt.Errorf("%w: unknown plugin subcommand %q", ErrUsage, args[0])
	}
}

// pluginInstall registers the proxy with herdr.
func pluginInstall(env Env, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%w: plugin install takes no arguments", ErrUsage)
	}

	if err := herdrOnPath(); err != nil {
		return err
	}

	manifest, err := manifestDir()
	if err != nil {
		return err
	}

	// `plugin link` registers a working directory and works with no herdr server
	// running, which is why this needs no daemon and no ordering.
	if out, err := runHerdr("plugin", "link", manifest); err != nil {
		return fmt.Errorf("linking the plugin: %w\n%s", err, out)
	}

	fmt.Fprintf(env.Out, "linked %s from %s\n", pluginID, manifest)

	// The pane runs `luna`, resolved against herdr's PATH rather than the shell
	// this command was typed in. Saying so now beats the person meeting "No
	// viable candidates found in PATH" when they open it.
	if _, err := exec.LookPath("luna"); err != nil {
		fmt.Fprintf(env.Out, "\nnote: `luna` is not on your PATH.\n")
		fmt.Fprintf(env.Out, "the pane falls back to the binary in this checkout, so it works from here.\n")
		fmt.Fprintf(env.Out, "install luna on your PATH to open it from anywhere.\n")
	}

	fmt.Fprintf(env.Out, "\nopen it with:\n")
	fmt.Fprintf(env.Out, "  herdr plugin pane open --plugin %s --entrypoint chat --placement split\n", pluginID)
	return nil
}

// pluginUninstall unregisters the proxy, leaving the files alone.
//
// `unlink` rather than `uninstall`: the manifest lives in this repository, and
// herdr's uninstall is for checkouts it manages itself. Removing the files would
// mean deleting part of the project.
func pluginUninstall(env Env, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%w: plugin uninstall takes no arguments", ErrUsage)
	}

	if err := herdrOnPath(); err != nil {
		return err
	}

	// Unlinking needs a running server, unlike linking — verified against 0.8.0,
	// where the documentation says both work without one. Saying so beats passing
	// on `server_not_running`, which reads as a Luna failure.
	if out, err := runHerdr("plugin", "unlink", pluginID); err != nil {
		if strings.Contains(out, "server_not_running") {
			return errors.New("herdr is not running, and unlinking needs it — start herdr and try again")
		}
		return fmt.Errorf("unlinking the plugin: %w\n%s", err, out)
	}

	fmt.Fprintf(env.Out, "unlinked %s — the files in this repository are untouched\n", pluginID)
	return nil
}

// pluginStatus reports whether herdr knows about the proxy.
func pluginStatus(env Env, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%w: plugin status takes no arguments", ErrUsage)
	}

	if err := herdrOnPath(); err != nil {
		return err
	}

	out, err := runHerdr("plugin", "list")
	if err != nil {
		return fmt.Errorf("asking herdr what it has: %w\n%s", err, out)
	}

	if !strings.Contains(out, pluginID) {
		fmt.Fprintf(env.Out, "%s is not installed — add it with `luna plugin install`\n", pluginID)
		return nil
	}

	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, pluginID) {
			fmt.Fprintln(env.Out, strings.TrimSpace(line))
		}
	}
	return nil
}

// herdrOnPath reports the one prerequisite, in Luna's words rather than the
// shell's.
func herdrOnPath() error {
	if !herdrInstalled() {
		return errors.New("herdr is not on your PATH, and the proxy is a herdr plugin — " +
			"`luna chat` works in any terminal without it")
	}
	return nil
}

// manifestDir finds the plugin directory shipped with this repository.
//
// It looks beside the running binary first, then at the working directory, so it
// works both from a checkout and from an installed copy. Failing to find it is
// reported with what was tried, because the alternative is herdr complaining
// about a path the person never typed.
var manifestDir = func() (string, error) {
	var tried []string

	if binary, err := os.Executable(); err == nil {
		// bin/luna → the repository root is two levels up.
		candidate := filepath.Join(filepath.Dir(binary), "..", "plugin")
		if found, ok := hasManifest(candidate); ok {
			return found, nil
		}
		tried = append(tried, candidate)
	}

	if cwd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(cwd, "plugin")
		if found, ok := hasManifest(candidate); ok {
			return found, nil
		}
		tried = append(tried, candidate)
	}

	return "", fmt.Errorf("cannot find the plugin manifest (looked in %s)", strings.Join(tried, ", "))
}

// hasManifest reports whether a directory holds the plugin, resolved absolutely
// because herdr records the path it is given.
func hasManifest(dir string) (string, bool) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(absolute, "herdr-plugin.toml")); err != nil {
		return "", false
	}
	return absolute, true
}

// runHerdr calls herdr and returns what it said, output included on failure —
// herdr's own message is usually the useful part.
//
// It is a variable so a test can answer without a herdr installed. The commands
// are the contract here, and a test that could not check which ones were sent
// would be checking nothing.
var runHerdr = func(args ...string) (string, error) {
	out, err := exec.Command("herdr", args...).CombinedOutput() //nolint:gosec // fixed arguments
	return string(out), err
}

// herdrInstalled reports whether herdr can be called at all. A variable for the
// same reason as runHerdr.
var herdrInstalled = func() bool {
	_, err := exec.LookPath("herdr")
	return err == nil
}
