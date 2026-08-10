package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAMissingConfigIsTheOrdinaryCase covers the default.
//
// Most projects will never write one. Treating its absence as an error would make
// every command fail until someone created an empty file.
func TestAMissingConfigIsTheOrdinaryCase(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "nothing-here.toml"))
	if err != nil {
		t.Fatalf("a missing config is not an error: %v", err)
	}
	if cfg.Editor != "" {
		t.Errorf("want no editor configured, got %q", cfg.Editor)
	}

	// The profiles are not zero, though: a project with no config still gets the
	// three shipped ones, or `--profile nightly` would stop working.
	if _, ok := cfg.Profile("nightly"); !ok {
		t.Errorf("want the shipped profiles, got %v", cfg.ProfileNames())
	}
}

// TestTheProjectCanSetItsOwnEditor covers the setting that motivated the file.
func TestTheProjectCanSetItsOwnEditor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	write(t, path, `
# how this project prefers to review contracts
editor = "code --wait"
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Editor != "code --wait" {
		t.Errorf("want the configured editor, got %q", cfg.Editor)
	}
}

// TestAnUnknownSettingIsAnError covers the strict-parsing decision.
//
// A typo in `editor` would otherwise leave the setting silently unapplied, and the
// person would conclude the feature does not work rather than that they misspelled
// it.
func TestAnUnknownSettingIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	write(t, path, `editr = "vim"`)

	_, err := LoadConfig(path)

	if err == nil {
		t.Fatal("an unknown setting must be reported")
	}
	if !strings.Contains(err.Error(), "editr") {
		t.Errorf("the error should name the offending key, got %v", err)
	}
}

// TestAMalformedLineIsReportedWithItsNumber covers the parse error path.
func TestAMalformedLineIsReportedWithItsNumber(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	write(t, path, "editor = \"vim\"\nthis is not a setting\n")

	_, err := LoadConfig(path)

	if err == nil {
		t.Fatal("a malformed line must be reported")
	}
	if !strings.Contains(err.Error(), ":2:") {
		t.Errorf("the error should name the line, got %v", err)
	}
}

// TestAnUnreadableConfigIsReported covers the I/O error path.
func TestAnUnreadableConfigIsReported(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions, so this cannot be provoked")
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	write(t, path, `editor = "vim"`)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("preparing the fixture: %v", err)
	}

	if _, err := LoadConfig(path); err == nil {
		t.Error("a config that cannot be read must be reported, not ignored")
	}
}

// TestConfigSitsNextToTheStore covers the path convention.
func TestConfigSitsNextToTheStore(t *testing.T) {
	got := ConfigPath("/home/someone/project/.luna/luna.db")

	if got != "/home/someone/project/.luna/config.toml" {
		t.Errorf("want the config beside the store, got %q", got)
	}
}

// TestEditorPrecedence covers the whole resolution order.
//
// The project's setting wins over the person's because $EDITOR serves their git,
// not this project. LUNA_EDITOR sits between the two so one invocation can
// override the project without editing a versioned file.
func TestEditorPrecedence(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		lunaEditor string
		editor     string
		visual     string
		want       string
	}{
		{"project beats everything", "project-editor", "luna-editor", "editor", "visual", "project-editor"},
		{"LUNA_EDITOR beats the personal ones", "", "luna-editor", "editor", "visual", "luna-editor"},
		{"EDITOR beats VISUAL", "", "", "editor", "visual", "editor"},
		{"VISUAL is the last resort", "", "", "", "visual", "visual"},
		{"nothing configured", "", "", "", "", ""},
		{"whitespace does not count as configured", "   ", "", "", "vim", "vim"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("LUNA_EDITOR", c.lunaEditor)
			t.Setenv("EDITOR", c.editor)
			t.Setenv("VISUAL", c.visual)

			if got := resolveEditor(c.configured); got != c.want {
				t.Errorf("want %q, got %q", c.want, got)
			}
		})
	}
}

// TestTheConfiguredEditorIsActuallyUsed covers the wiring, not just the resolution.
func TestTheConfiguredEditorIsActuallyUsed(t *testing.T) {
	t.Setenv("EDITOR", "false") // would fail if it were the one picked
	t.Setenv("LUNA_EDITOR", "")
	t.Setenv("VISUAL", "")

	edit := Editor(Config{Editor: "cat"})
	if edit == nil {
		t.Fatal("a configured editor must produce an edit function")
	}

	got, err := edit("the contract")
	if err != nil {
		t.Fatalf("the project's editor should have been used: %v", err)
	}
	if got != "the contract" {
		t.Errorf("want the content back unchanged, got %q", got)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
}
