package store_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/brunoomariano/luna/src/internal/store"
)

func settingStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.OpenAs(filepath.Join(t.TempDir(), "luna.db"), store.LunaOwnsTheLog)
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestTheCurrentValueIsTheLastOneWritten is the floor: set twice, read the
// second.
func TestTheCurrentValueIsTheLastOneWritten(t *testing.T) {
	s := settingStore(t)

	for _, value := range []string{"luna", "the-project"} {
		if err := s.PutSetting("app-1", "workstream", value); err != nil {
			t.Fatalf("setting workstream to %q: %v", value, err)
		}
	}

	current, err := s.Settings("app-1")
	if err != nil {
		t.Fatalf("reading the settings: %v", err)
	}
	if current["workstream"] != "the-project" {
		t.Errorf("workstream reads %q, want the value set last", current["workstream"])
	}
}

// TestChangingASettingKeepsWhatItWas is what the table is append-only for.
//
// The file this replaced lived in git, so "who changed the workstream, and when"
// was answered by the commit that changed it. A row that is overwritten answers
// it with the current value and nothing else, which is trading an audit for a
// lookup.
func TestChangingASettingKeepsWhatItWas(t *testing.T) {
	s := settingStore(t)

	for _, value := range []string{"luna", "spike-tls", "the-project"} {
		if err := s.PutSetting("app-1", "workstream", value); err != nil {
			t.Fatalf("setting workstream: %v", err)
		}
	}

	history, err := s.SettingHistory("app-1", "workstream")
	if err != nil {
		t.Fatalf("reading the history: %v", err)
	}

	want := []string{"luna", "spike-tls", "the-project"}
	if len(history) != len(want) {
		t.Fatalf("the history has %d entries, want %d: %v", len(history), len(want), history)
	}
	for i, value := range want {
		if history[i].Value != value {
			t.Errorf("entry %d is %q, want %q — the history is not what was written", i, history[i].Value, value)
		}
		if history[i].Seq != i {
			t.Errorf("entry %d has seq %d, want %d: nothing orders two values without it", i, history[i].Seq, i)
		}
	}
}

// TestUnsettingLeavesTheRowAndHidesTheKey keeps two facts apart.
//
// "This project deliberately falls back to the machine's setting" and "nobody
// ever said anything here" are different, and deleting the row would make them
// the same. The reader sees the key as unset; the history still shows the value
// it held and that somebody cleared it.
func TestUnsettingLeavesTheRowAndHidesTheKey(t *testing.T) {
	s := settingStore(t)

	if err := s.PutSetting("app-1", "workstream", "spike-tls"); err != nil {
		t.Fatalf("setting: %v", err)
	}
	if err := s.PutSetting("app-1", "workstream", ""); err != nil {
		t.Fatalf("unsetting: %v", err)
	}

	current, err := s.Settings("app-1")
	if err != nil {
		t.Fatalf("reading the settings: %v", err)
	}
	if _, present := current["workstream"]; present {
		t.Errorf("an unset key is still reported as configured: %v", current)
	}

	history, err := s.SettingHistory("app-1", "workstream")
	if err != nil {
		t.Fatalf("reading the history: %v", err)
	}
	if len(history) != 2 || history[0].Value != "spike-tls" || history[1].Value != "" {
		t.Errorf("unsetting lost the history rather than adding to it: %v", history)
	}
}

// TestScopesDoNotSeeEachOther. A project setting and the machine's own share a
// key name on purpose — that is what falling back means — so the store has to
// keep them apart and let the caller decide which wins.
func TestScopesDoNotSeeEachOther(t *testing.T) {
	s := settingStore(t)

	if err := s.PutSetting(store.GlobalScope, "editor", "vim"); err != nil {
		t.Fatalf("setting the global editor: %v", err)
	}
	if err := s.PutSetting("app-1", "editor", "hx"); err != nil {
		t.Fatalf("setting the project editor: %v", err)
	}

	global, err := s.Settings(store.GlobalScope)
	if err != nil {
		t.Fatalf("reading the global settings: %v", err)
	}
	project, err := s.Settings("app-1")
	if err != nil {
		t.Fatalf("reading the project settings: %v", err)
	}

	if global["editor"] != "vim" || project["editor"] != "hx" {
		t.Errorf("one scope read the other's value: global=%q project=%q", global["editor"], project["editor"])
	}

	scopes, err := s.SettingScopes()
	if err != nil {
		t.Fatalf("reading the scopes: %v", err)
	}
	if len(scopes) != 2 || scopes[0] != store.GlobalScope || scopes[1] != "app-1" {
		t.Errorf("the configured scopes are %v, want the global one and app-1", scopes)
	}
}

// TestAKeyNothingSetIsSaidToBeMissing rather than answered with an empty value:
// a caller asking for the history of a typo should be told it is a typo.
func TestAKeyNothingSetIsSaidToBeMissing(t *testing.T) {
	s := settingStore(t)

	_, err := s.SettingHistory("app-1", "wrokstream")

	if !errors.Is(err, store.ErrNoSuchSetting) {
		t.Errorf("a key nothing set answered %v, want ErrNoSuchSetting", err)
	}
}

// TestAReaderCannotWriteASetting. The daemon is the only writer, and a setting
// is state like any other — a CLI process that could write one would be a second
// way into the database (INV-2).
func TestAReaderCannotWriteASetting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "luna.db")
	writer, err := store.OpenAs(path, store.LunaOwnsTheLog)
	if err != nil {
		t.Fatalf("opening the writer: %v", err)
	}
	_ = writer.Close()

	reader, err := store.Open(path)
	if err != nil {
		t.Fatalf("opening the reader: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	if err := reader.PutSetting("app-1", "workstream", "sneaked"); err == nil {
		t.Error("a read-only store wrote a setting, so the daemon is not the only writer")
	}
}
