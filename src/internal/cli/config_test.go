package cli

import (
	"testing"
)

// TestNothingConfiguredIsTheOrdinaryCase covers the default.
//
// Most projects will never set anything. Treating that as an error would make
// every command fail until somebody configured something — and the profiles are
// not empty either: a project that declared none still gets the three shipped
// ones, or `--profile nightly` would stop working.
func TestNothingConfiguredIsTheOrdinaryCase(t *testing.T) {
	cfg, err := ConfigFrom(nil, nil)
	if err != nil {
		t.Fatalf("an unconfigured project is not an error: %v", err)
	}
	if cfg.Editor != "" {
		t.Errorf("want no editor configured, got %q", cfg.Editor)
	}
	if !cfg.Defines("nightly") {
		t.Errorf("want the shipped profiles, got %v", cfg.ProfileNames())
	}
}

// TestASettingThatCannotBeReadBackIsRefused covers the strict reading.
//
// A key nothing understands, sitting in the database, is a setting that looks
// applied and does nothing. `luna config set` refuses it at the terminal; this is
// the other end, where a database written by another build is read.
func TestASettingThatCannotBeReadBackIsRefused(t *testing.T) {
	if _, err := ConfigFrom(nil, map[string]string{"edtior": "hx"}); err == nil {
		t.Error("a key nothing reads was accepted")
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
