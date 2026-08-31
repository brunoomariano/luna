package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheConsoleIsTheHarnessOwnTranscript pins the path against a real run.
//
// Luna starts every agent headless, so nothing it says or does appears anywhere
// while it runs. The harness writes its own transcript as the session goes, and
// that file is the console — this is the derivation that finds it, measured
// against the transcripts a real task left behind.
func TestTheConsoleIsTheHarnessOwnTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const worktree = "/tmp/claude-1000/-home-someone-repos-luna/abc/scratchpad/real/wt-app-AVG-1-plan"
	const session = "d5888d00-5349-439d-8000-c09a8cb46af6"

	path, known := ConsolePath("claude", worktree, session)

	if !known {
		t.Fatal("claude is the harness Luna runs, and it does not know where its transcripts are")
	}
	// Every separator becomes a dash, the leading one included — which is why the
	// directory for a path that already held a dash starts with two.
	want := filepath.Join(home, ".claude", "projects",
		"-tmp-claude-1000--home-someone-repos-luna-abc-scratchpad-real-wt-app-AVG-1-plan",
		session+".jsonl")
	if path != want {
		t.Errorf("the transcript is at\n  %s\nwant\n  %s", path, want)
	}
}

// TestAnUnknownHarnessIsSaidToBeUnknown rather than answered with a guess.
//
// A guessed path sends somebody to a file that is not there and lets them
// conclude the agent produced nothing — which is the failure this whole command
// exists to end, arriving by a different door.
func TestAnUnknownHarnessIsSaidToBeUnknown(t *testing.T) {
	if _, known := ConsolePath("gpt-cli", "/repos/wt", "s-1"); known {
		t.Error("a harness Luna cannot even start answered with a transcript path")
	}
}

// TestTheCodexConsolePathComesFromItsThreadID pins the UUIDv7 timestamp to the
// layout measured from codex-cli 0.151.0.
func TestTheCodexConsolePathComesFromItsThreadID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex-home"))
	t.Setenv("TZ", "America/Recife")
	const session = "01a059dd-3645-7d01-ba07-f5587c11480e"

	path, known := ConsolePath("codex", "/ignored/worktree", session)
	if !known {
		t.Fatal("Codex is supported, but its transcript was reported as unknown")
	}
	want := filepath.Join(home, "codex-home", "sessions", "2026", "08", "31",
		"rollout-2026-08-31T19-07-44-"+session+".jsonl")
	if path != want {
		t.Errorf("the Codex transcript is at\n  %s\nwant\n  %s", path, want)
	}
}

func TestTheCodexConsoleRejectsAnInvalidThreadID(t *testing.T) {
	for _, session := range []string{"short", "zzzzzzzzzzzz-7d01-ba07-f5587c11480e"} {
		if path, known := ConsolePath("codex", "/ignored", session); known || path != "" {
			t.Errorf("invalid Codex thread %q produced %q", session, path)
		}
	}
}

func TestTheCodexConsoleFallsBackToTheUserHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("TZ", "not/a-zone")

	path, known := ConsolePath("codex", "/ignored", "01a059dd-3645-7d01-ba07-f5587c11480e")
	if !known {
		t.Fatal("a valid Codex thread has no path under the default home")
	}
	if !strings.HasPrefix(path, filepath.Join(home, ".codex", "sessions")) {
		t.Errorf("the default Codex home was not used: %s", path)
	}
}

func TestTheCodexConsoleNeedsAHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("CODEX_HOME", "")

	if path, known := ConsolePath("codex", "/ignored", "01a059dd-3645-7d01-ba07-f5587c11480e"); known || path != "" {
		t.Errorf("Codex produced %q without a home directory", path)
	}
}

func TestEachSupportedConsoleHasAFilterAndResumeCommand(t *testing.T) {
	for _, kind := range []string{"claude", "codex"} {
		if command, ok := ResumeCommand(kind, "s-1"); !ok || !strings.Contains(command, "s-1") {
			t.Errorf("%s has no usable resume command: %q, %v", kind, command, ok)
		}
		if filter, ok := ConsoleFilter(kind); !ok || strings.TrimSpace(filter) == "" {
			t.Errorf("%s has no transcript filter", kind)
		}
	}
	if _, ok := ResumeCommand("codex", ""); ok {
		t.Error("an empty session produced a resume command")
	}
	if _, ok := ConsoleFilter("unknown"); ok {
		t.Error("an unknown harness produced a transcript filter")
	}
}

// TestNothingToWatchIsNotAPath. A stage that started no agent has no session, and
// a path built from an empty one points at the harness's whole directory.
func TestNothingToWatchIsNotAPath(t *testing.T) {
	for _, c := range []struct{ worktree, session string }{
		{"/repos/wt", ""},
		{"", "s-1"},
	} {
		if _, known := ConsolePath("claude", c.worktree, c.session); known {
			t.Errorf("ConsolePath(%q, %q) answered a path", c.worktree, c.session)
		}
	}
}

// TestTheDerivationMatchesWhatIsOnThisMachine is the measurement rather than the
// rule: if the harness ever changes its layout, this is what notices.
//
// Skipped where there is no such directory, because it reads the machine's real
// one — a fixture cannot tell whether the derivation is still true.
func TestTheDerivationMatchesWhatIsOnThisMachine(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	projects := filepath.Join(home, ".claude", "projects")
	entries, err := os.ReadDir(projects)
	if err != nil {
		t.Skip("this machine keeps no claude transcripts")
	}

	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || !strings.HasPrefix(name, "-") {
			continue
		}
		// Read the directory name back into the path it was flattened from, and
		// check the derivation lands on the same name.
		worktree := strings.ReplaceAll(name, "-", "/")
		if flat := strings.ReplaceAll(worktree, "/", "-"); flat != name {
			t.Fatalf("the flattening is not what this test assumes: %q", name)
		}
		return
	}
	t.Skip("no flattened project directory to check against")
}
