package cli

import (
	"os"
	"strings"
	"testing"
)

// TestEditorIsNilWithoutAnEnvironment covers the constructor's guard.
//
// Returning nil rather than a function that always fails is what lets the command
// say "set $EDITOR" instead of launching nothing and reporting a crash.
func TestEditorIsNilWithoutAnEnvironment(t *testing.T) {
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")
	t.Setenv("LUNA_EDITOR", "")

	if Editor(Config{}) != nil {
		t.Error("with no editor configured there is nothing to return")
	}
}

func TestEditorFallsBackToVisual(t *testing.T) {
	t.Setenv("EDITOR", "")
	t.Setenv("LUNA_EDITOR", "")
	t.Setenv("VISUAL", "true")

	if Editor(Config{}) == nil {
		t.Error("$VISUAL is the traditional fallback and must be honoured")
	}
}

// TestEditInEditorRoundTripsThroughTheFile covers the happy path.
//
// `cat` as the editor is a program that opens the file and changes nothing, which
// is exactly the "left it alone" case — and it proves the content survives the
// round trip to disk and back.
func TestEditInEditorRoundTripsThroughTheFile(t *testing.T) {
	t.Setenv("EDITOR", "cat")

	const original = "the generated contract\nwith two lines\n"
	got, err := EditInEditor("", original)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != original {
		t.Errorf("an editor that changes nothing returns what it was given:\nwant %q\ngot  %q", original, got)
	}
}

// TestEditInEditorSeesTheEdit covers the path that matters.
//
// `sed -i` stands in for a person who changed something: the function has to read
// back what was left on disk, not what it wrote there.
func TestEditInEditorSeesTheEdit(t *testing.T) {
	if _, err := os.Stat("/usr/bin/sed"); err != nil {
		t.Skip("sed is not available to stand in for an editor")
	}
	t.Setenv("EDITOR", "sed -i s/generated/reviewed/")

	got, err := EditInEditor("", "the generated contract\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(got, "reviewed") {
		t.Errorf("want the edit read back, got %q", got)
	}
}

func TestEditInEditorWithoutAnEditorSaysSo(t *testing.T) {
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")
	t.Setenv("LUNA_EDITOR", "")

	_, err := EditInEditor("", "anything")

	if err == nil || !strings.Contains(err.Error(), "config.toml") {
		t.Errorf("want a message naming what to set, got %v", err)
	}
}

// TestEditInEditorReportsAFailingEditor covers the non-zero exit path.
//
// An editor that dies must not be read as an approval of whatever happened to be
// in the file.
func TestEditInEditorReportsAFailingEditor(t *testing.T) {
	t.Setenv("EDITOR", "false")

	if _, err := EditInEditor("", "anything"); err == nil {
		t.Error("an editor exiting non-zero must be reported")
	}
}
