package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPluginNeedsASubcommand covers the surface.
func TestPluginNeedsASubcommand(t *testing.T) {
	h := newHarness(t)

	for _, args := range [][]string{{"plugin"}, {"plugin", "instal"}} {
		if err := h.run(t, args...); err == nil {
			t.Errorf("%v must be reported", args)
		}
	}
}

// TestPluginSubcommandsTakeNoArguments covers the three of them.
func TestPluginSubcommandsTakeNoArguments(t *testing.T) {
	h := newHarness(t)

	for _, sub := range []string{"install", "uninstall", "status"} {
		if err := h.run(t, "plugin", sub, "extra"); err == nil {
			t.Errorf("plugin %s takes no arguments", sub)
		}
	}
}

// TestWithoutHerdrTheProxyIsReportedAsOptional covers the prerequisite.
//
// The proxy is a convenience, and someone without herdr should hear that `luna
// chat` works anyway rather than that something is broken.
//
// Every subcommand, not only install: the machine that installed the proxy is
// not necessarily the one uninstalling it, and someone who removed herdr and
// then ran `luna plugin uninstall` to tidy up would otherwise meet an exec
// failure about a binary they deliberately deleted.
func TestWithoutHerdrTheProxyIsReportedAsOptional(t *testing.T) {
	for _, sub := range []string{"install", "uninstall", "status"} {
		t.Run(sub, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())
			h := newHarness(t)

			err := h.run(t, "plugin", sub)

			if err == nil {
				t.Fatalf("with no herdr, plugin %s must be reported", sub)
			}
			if !strings.Contains(err.Error(), "luna chat") {
				t.Errorf("the message should name the fallback, got %v", err)
			}
		})
	}
}

// TestTheManifestIsFoundFromTheCheckout covers the lookup.
//
// Failing to find it must say what was tried: the alternative is herdr
// complaining about a path the person never typed.
func TestTheManifestIsFoundFromTheCheckout(t *testing.T) {
	dir := t.TempDir()
	plugin := filepath.Join(dir, "plugin")
	if err := os.MkdirAll(plugin, 0o750); err != nil {
		t.Fatalf("setting up: %v", err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "herdr-plugin.toml"), []byte("id = \"x\"\n"), 0o600); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	found, ok := hasManifest(plugin)
	if !ok {
		t.Fatal("a directory holding the manifest must be found")
	}
	if !filepath.IsAbs(found) {
		t.Errorf("herdr records the path it is given, so it must be absolute: %q", found)
	}

	if _, ok := hasManifest(filepath.Join(dir, "nothing-here")); ok {
		t.Error("a directory without the manifest is not the plugin")
	}
}

// TestTheShippedManifestIsWhereTheCommandLooks guards the seam between the
// command and the repository layout.
//
// Moving `plugin/` without telling the command would leave the install failing
// with a path nobody typed.
func TestTheShippedManifestIsWhereTheCommandLooks(t *testing.T) {
	// The test binary runs in the package directory, so the repository root is
	// three levels up: src/internal/cli.
	root := filepath.Join("..", "..", "..")

	if _, ok := hasManifest(filepath.Join(root, "plugin")); !ok {
		t.Error("the repository must ship plugin/herdr-plugin.toml where the command looks for it")
	}
}

// withFakeHerdr answers herdr's commands from a table and records what was asked,
// so a test can check which commands were sent without a herdr installed.
func withFakeHerdr(t *testing.T, answers map[string]string, fails map[string]bool) *[][]string {
	t.Helper()

	var asked [][]string
	realRun, realInstalled, realManifest := runHerdr, herdrInstalled, manifestDir

	// The manifest lives in the repository, and a test binary runs from neither
	// the checkout nor an install — so the lookup is stubbed rather than the test
	// being made to care where `go test` put it.
	manifestDir = func() (string, error) { return filepath.Join(t.TempDir(), "plugin"), nil }

	herdrInstalled = func() bool { return true }
	runHerdr = func(args ...string) (string, error) {
		asked = append(asked, args)
		key := strings.Join(args[:2], " ")
		if fails[key] {
			return answers[key], errors.New("exit status 1")
		}
		return answers[key], nil
	}

	t.Cleanup(func() { runHerdr, herdrInstalled, manifestDir = realRun, realInstalled, realManifest })
	return &asked
}

// TestInstallLinksTheManifestAndSaysHowToOpenIt covers the happy path.
//
// It prints the command it ran and the one to run next, because the point is not
// to hide herdr but to stop three steps from failing in herdr's words.
func TestInstallLinksTheManifestAndSaysHowToOpenIt(t *testing.T) {
	asked := withFakeHerdr(t, map[string]string{"plugin link": "linked"}, nil)
	h := newHarness(t)

	out := h.mustRun(t, "plugin", "install")

	if len(*asked) != 1 || strings.Join((*asked)[0][:2], " ") != "plugin link" {
		t.Fatalf("want one `plugin link`, got %v", *asked)
	}
	// The path it links must be the directory holding the manifest, absolute,
	// because herdr records what it is given.
	linked := (*asked)[0][2]
	if !filepath.IsAbs(linked) || !strings.HasSuffix(linked, "plugin") {
		t.Errorf("want the absolute manifest directory, got %q", linked)
	}
	if !strings.Contains(out, "pane open") {
		t.Errorf("the person must be told how to open it, got %q", out)
	}
}

// TestInstallWarnsWhenLunaIsNotOnPath covers the failure that actually happened.
//
// herdr resolves a pane's argv against its own PATH, so a checkout with no
// installed luna meets "No viable candidates found in PATH" — which reads as a
// broken plugin. Saying it at install time is cheaper than at open time.
func TestInstallWarnsWhenLunaIsNotOnPath(t *testing.T) {
	withFakeHerdr(t, map[string]string{"plugin link": "linked"}, nil)
	t.Setenv("PATH", t.TempDir()) // no luna anywhere
	h := newHarness(t)

	out := h.mustRun(t, "plugin", "install")

	if !strings.Contains(out, "PATH") {
		t.Errorf("the note must mention PATH, got %q", out)
	}
	if !strings.Contains(out, "falls back") {
		t.Errorf("it must say the pane still works from here, got %q", out)
	}
}

// TestUninstallUnlinksRatherThanDeleting covers the choice of verb.
//
// The manifest lives in this repository; herdr's uninstall removes checkouts it
// manages. Deleting the files would mean deleting part of the project.
func TestUninstallUnlinksRatherThanDeleting(t *testing.T) {
	asked := withFakeHerdr(t, map[string]string{"plugin unlink": "unlinked"}, nil)
	h := newHarness(t)

	out := h.mustRun(t, "plugin", "uninstall")

	if len(*asked) != 1 || (*asked)[0][1] != "unlink" {
		t.Fatalf("want `plugin unlink`, got %v", *asked)
	}
	if !strings.Contains(out, "untouched") {
		t.Errorf("the person must hear the files stay, got %q", out)
	}
}

// TestUnlinkingWithoutAServerSaysSo covers a difference from the documentation.
//
// herdr's docs say both link and unlink work with no server; only link does,
// verified against 0.8.0. Passing on `server_not_running` would read as a Luna
// failure.
func TestUnlinkingWithoutAServerSaysSo(t *testing.T) {
	withFakeHerdr(t,
		map[string]string{"plugin unlink": `{"error":{"code":"server_not_running"}}`},
		map[string]bool{"plugin unlink": true})
	h := newHarness(t)

	err := h.run(t, "plugin", "uninstall")

	if err == nil {
		t.Fatal("unlinking without a server must be reported")
	}
	if !strings.Contains(err.Error(), "herdr is not running") {
		t.Errorf("the message must name the cause, got %v", err)
	}
	if strings.Contains(err.Error(), "server_not_running") {
		t.Errorf("herdr's raw code is not the answer, got %v", err)
	}
}

// TestStatusReportsWhatHerdrHas covers both answers.
func TestStatusReportsWhatHerdrHas(t *testing.T) {
	withFakeHerdr(t, map[string]string{
		"plugin list": "1 plugin installed:\n- luna.chat (Luna) enabled [local:/x]\n",
	}, nil)
	h := newHarness(t)

	if out := h.mustRun(t, "plugin", "status"); !strings.Contains(out, "luna.chat") {
		t.Errorf("want the plugin's line, got %q", out)
	}

	withFakeHerdr(t, map[string]string{"plugin list": "no plugins installed\n"}, nil)
	out := h.mustRun(t, "plugin", "status")

	if !strings.Contains(out, "not installed") {
		t.Errorf("want it reported as absent, got %q", out)
	}
	if !strings.Contains(out, "luna plugin install") {
		t.Errorf("it should say how to fix that, got %q", out)
	}
}

// TestALinkFailureCarriesHerdrsOwnMessage covers the pass-through.
//
// herdr's message is usually the useful part; swallowing it would leave someone
// with "linking the plugin failed" and nothing to act on.
func TestALinkFailureCarriesHerdrsOwnMessage(t *testing.T) {
	withFakeHerdr(t,
		map[string]string{"plugin link": "manifest is not valid TOML at line 4"},
		map[string]bool{"plugin link": true})
	h := newHarness(t)

	err := h.run(t, "plugin", "install")

	if err == nil {
		t.Fatal("a failed link must be reported")
	}
	if !strings.Contains(err.Error(), "line 4") {
		t.Errorf("herdr's own message must survive, got %v", err)
	}
}

// TestTheManifestLookupSaysWhatItTried covers the failure a person would
// otherwise meet as herdr complaining about a path they never typed.
func TestTheManifestLookupSaysWhatItTried(t *testing.T) {
	// A directory with no plugin/ in it, and nothing beside the test binary
	// either — which is what an installed luna without its manifest looks like.
	t.Chdir(t.TempDir())

	_, err := manifestDir()

	if err == nil {
		t.Fatal("a missing manifest must be reported")
	}
	if !strings.Contains(err.Error(), "looked in") {
		t.Errorf("the error must say where it looked, got %v", err)
	}
}

// TestTheManifestIsFoundBesideTheWorkingDirectory covers the branch that makes
// `luna plugin install` work from a checkout.
func TestTheManifestIsFoundBesideTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	plugin := filepath.Join(dir, "plugin")
	if err := os.MkdirAll(plugin, 0o750); err != nil {
		t.Fatalf("setting up: %v", err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "herdr-plugin.toml"), []byte("id = \"x\"\n"), 0o600); err != nil {
		t.Fatalf("setting up: %v", err)
	}
	t.Chdir(dir)

	found, err := manifestDir()
	if err != nil {
		t.Fatalf("the checkout's manifest must be found: %v", err)
	}
	if !strings.HasSuffix(found, "plugin") {
		t.Errorf("want the plugin directory, got %q", found)
	}
}

// TestUninstallCarriesAnUnexpectedFailure covers the branch that is not the
// missing server.
func TestUninstallCarriesAnUnexpectedFailure(t *testing.T) {
	withFakeHerdr(t,
		map[string]string{"plugin unlink": "plugin luna.chat is not registered"},
		map[string]bool{"plugin unlink": true})
	h := newHarness(t)

	err := h.run(t, "plugin", "uninstall")

	if err == nil {
		t.Fatal("a failed unlink must be reported")
	}
	if !strings.Contains(err.Error(), "not registered") {
		t.Errorf("herdr's own message must survive, got %v", err)
	}
}

// TestStatusCarriesAFailureFromHerdr covers the last branch.
func TestStatusCarriesAFailureFromHerdr(t *testing.T) {
	withFakeHerdr(t,
		map[string]string{"plugin list": "the registry is corrupt"},
		map[string]bool{"plugin list": true})
	h := newHarness(t)

	if err := h.run(t, "plugin", "status"); err == nil {
		t.Error("a failed list must be reported")
	}
}

// TestInstallWithNoManifestStopsBeforeCallingHerdr is the ordering that keeps
// the failure legible.
//
// The manifest is looked up before herdr is asked anything. An install that
// called `herdr plugin link ""` first would fail in herdr's words about a path
// nobody typed — and worse, it would leave the person believing herdr is the
// broken part when what is actually missing is the plugin directory that ships
// with this repository.
func TestInstallWithNoManifestStopsBeforeCallingHerdr(t *testing.T) {
	asked := withFakeHerdr(t, nil, nil)
	realManifest := manifestDir
	manifestDir = func() (string, error) {
		return "", errors.New("cannot find the plugin manifest (looked in /nowhere)")
	}
	t.Cleanup(func() { manifestDir = realManifest })

	h := newHarness(t)
	err := h.run(t, "plugin", "install")

	if err == nil {
		t.Fatal("installing without a manifest must be reported")
	}
	if !strings.Contains(err.Error(), "looked in") {
		t.Errorf("the failure must carry where it looked, got %v", err)
	}
	if len(*asked) != 0 {
		t.Errorf("herdr was called with nothing to link: %v", *asked)
	}
}
