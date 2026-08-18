package node_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/brunoomariano/luna/src/internal/node"
)

func claudeConfig(t *testing.T, content string) (home, path string) {
	t.Helper()
	home = t.TempDir()
	path = filepath.Join(home, ".claude.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("setting up the config: %v", err)
	}
	return home, path
}

// TestTrustAddsTheParentAndKeepsEverythingElse is the merge rule: one key in,
// every other byte's meaning preserved. The first attempt replaced the file and
// was measured destroying a real config, OAuth included.
func TestTrustAddsTheParentAndKeepsEverythingElse(t *testing.T) {
	home, path := claudeConfig(t,
		`{"oauthAccount":{"id":"keep-me"},"theme":"dark","projects":{"/elsewhere":{"hasTrustDialogAccepted":false}}}`)
	repo := filepath.Join(t.TempDir(), "repo")

	parent, err := node.TrustWorktreeParent(home, repo)
	if err != nil {
		t.Fatalf("trusting: %v", err)
	}
	if parent != filepath.Dir(repo) {
		t.Errorf("the trusted directory is the worktrees' parent, got %q", parent)
	}

	raw, _ := os.ReadFile(path)
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("the result must stay valid JSON: %v", err)
	}
	oauth, ok := config["oauthAccount"].(map[string]any)
	if !ok || oauth["id"] != "keep-me" {
		t.Error("everything that was there survives the merge")
	}
	projects, ok := config["projects"].(map[string]any)
	if !ok {
		t.Fatalf("projects must survive as an object, got %T", config["projects"])
	}
	if entry, ok := projects[parent].(map[string]any); !ok || entry["hasTrustDialogAccepted"] != true {
		t.Errorf("the parent must be trusted, got %v", projects[parent])
	}
	if entry, ok := projects["/elsewhere"].(map[string]any); !ok || entry["hasTrustDialogAccepted"] != false {
		t.Error("another project's answer is the user's and stays as given")
	}
}

// TestTrustRefusesToInventTheConfig: a missing file means claude has never run,
// and a file Luna invents would be replaced by claude's own first start.
func TestTrustRefusesToInventTheConfig(t *testing.T) {
	home := t.TempDir()

	_, err := node.TrustWorktreeParent(home, filepath.Join(home, "repo"))
	if !errors.Is(err, node.ErrNoClaudeConfig) {
		t.Fatalf("a config that does not exist is not created, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".claude.json")); statErr == nil {
		t.Error("the refusal must not have created the file it refused over")
	}
}

// TestTrustRefusesAConfigItCannotParse: a file holding credentials is not one to
// fix by guessing.
func TestTrustRefusesAConfigItCannotParse(t *testing.T) {
	home, path := claudeConfig(t, `this is not json`)

	_, err := node.TrustWorktreeParent(home, filepath.Join(home, "repo"))
	if err == nil {
		t.Fatal("an unparseable config must be refused")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != `this is not json` {
		t.Error("a refused config is left exactly as found")
	}
}

// TestTrustIsIdempotent: already trusted means nothing to write, and the file's
// bytes are not churned for a no-op.
func TestTrustIsIdempotent(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	parent := filepath.Dir(repo)
	home, path := claudeConfig(t,
		`{"projects":{"`+parent+`":{"hasTrustDialogAccepted":true,"note":"kept"}}}`)
	before, _ := os.ReadFile(path)

	if _, err := node.TrustWorktreeParent(home, repo); err != nil {
		t.Fatalf("re-trusting: %v", err)
	}

	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("an already-trusted parent writes nothing")
	}
}

// TestTrustRefusesTheFilesystemRoot: trusting / is not a scope.
func TestTrustRefusesTheFilesystemRoot(t *testing.T) {
	home, _ := claudeConfig(t, `{}`)

	if _, err := node.TrustWorktreeParent(home, "/repo"); err == nil {
		t.Fatal("a repository at the root would trust the whole filesystem")
	}
}

// TestTrustReportsAnUnwritableReplacement covers the atomic write's own failure,
// so a permissions problem is named rather than swallowed.
func TestTrustReportsAnUnwritableReplacement(t *testing.T) {
	home, _ := claudeConfig(t, `{}`)
	repo := filepath.Join(t.TempDir(), "repo")

	if err := os.Chmod(home, 0o500); err != nil {
		t.Skipf("cannot make the home read-only here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })

	_, err := node.TrustWorktreeParent(home, repo)
	if err == nil {
		t.Fatal("a home that cannot be written must be reported")
	}
}

// TestTrustReportsAnUnreadableConfig separates "does not exist" from "cannot be
// read": the first has a way out the person can take, the second is a fault to
// name as itself.
func TestTrustReportsAnUnreadableConfig(t *testing.T) {
	home, path := claudeConfig(t, `{}`)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Skipf("cannot drop permissions here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	_, err := node.TrustWorktreeParent(home, filepath.Join(home, "repo"))
	if err == nil || errors.Is(err, node.ErrNoClaudeConfig) {
		t.Fatalf("an unreadable config is not a missing one, got %v", err)
	}
}

// TestTrustAddsToAConfigWithNoProjectsSection covers the config claude writes
// before any project was ever opened.
func TestTrustAddsToAConfigWithNoProjectsSection(t *testing.T) {
	home, path := claudeConfig(t, `{"theme":"dark"}`)
	repo := filepath.Join(t.TempDir(), "repo")

	parent, err := node.TrustWorktreeParent(home, repo)
	if err != nil {
		t.Fatalf("trusting: %v", err)
	}

	raw, _ := os.ReadFile(path)
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("the result must stay valid JSON: %v", err)
	}
	projects, ok := config["projects"].(map[string]any)
	if !ok {
		t.Fatalf("a projects section is born when the first trust needs it, got %T", config["projects"])
	}
	if entry, ok := projects[parent].(map[string]any); !ok || entry["hasTrustDialogAccepted"] != true {
		t.Errorf("the parent must be trusted, got %v", projects[parent])
	}
}

// TestTrustRefusesAProjectsSectionOfTheWrongShape: a projects key that is not an
// object is a config this build does not understand, and guessing at a file that
// holds credentials is not an option.
func TestTrustRefusesAProjectsSectionOfTheWrongShape(t *testing.T) {
	home, path := claudeConfig(t, `{"projects":"not an object"}`)

	_, err := node.TrustWorktreeParent(home, filepath.Join(home, "repo"))
	if err == nil {
		t.Fatal("a projects section of the wrong shape must be refused")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != `{"projects":"not an object"}` {
		t.Error("a refused config is left exactly as found")
	}
}
