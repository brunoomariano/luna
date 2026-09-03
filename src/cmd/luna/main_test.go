package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/ledger"
)

// The exit codes are the contract with whoever conducts: 0 proven, 2 not proven,
// 1 Luna could not run. A conductor that cannot tell the last two apart reads a
// broken machine as a failed delivery, so this exercises the real binary rather
// than the package behind it.
func TestTheBinarySeparatesAFailedDeliveryFromABrokenLuna(t *testing.T) {
	luna := build(t)
	repo, ledgerPath := workspace(t)

	green := contractFile(t, repo, `
phase    = "forge"
produces = ["ci_green"]

[verify.ci_green]
run   = "true"
scope = "full"
`)
	red := filepath.Join(repo, "red.toml")
	if err := os.WriteFile(red, []byte(`
phase    = "forge"
produces = ["ci_green"]

[verify.ci_green]
run   = "exit 1"
scope = "full"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	if code, out := run(t, luna, repo, ledgerPath, "check", "--contract", green, "--run", "E2E-1"); code != 0 {
		t.Errorf("a proven phase exited %d:\n%s", code, out)
	}
	if code, out := run(t, luna, repo, ledgerPath, "check", "--contract", red, "--run", "E2E-1"); code != 2 {
		t.Errorf("an unproven phase exited %d, want 2:\n%s", code, out)
	}
	if code, out := run(t, luna, repo, ledgerPath, "check", "--contract", "/nowhere.toml", "--run", "E2E-1"); code != 1 {
		t.Errorf("a contract that is not there exited %d, want 1:\n%s", code, out)
	}
	if code, _ := run(t, luna, repo, ledgerPath, "orchestrate"); code != 1 {
		t.Errorf("an unknown command exited %d, want 1", code)
	}
}

// The whole cycle a conductor runs, through the real binary: prove, record,
// block, read it back.
func TestTheBinaryRecordsACycleAndReadsItBack(t *testing.T) {
	luna := build(t)
	repo, ledgerPath := workspace(t)

	if code, out := run(t, luna, repo, ledgerPath,
		"record", "--run", "E2E-2", "--event", "phase", "--status", "running", "--phase", "forge"); code != 0 {
		t.Fatalf("recording exited %d:\n%s", code, out)
	}
	if code, out := run(t, luna, repo, ledgerPath,
		"record", "--run", "E2E-2", "--event", "block", "--status", "blocked", "--phase", "forge",
		"--question", "is --largest meant to return an argument?",
		"--looked", "the contract, clause 4 — undefined with --max",
		"--needs", "which of the two readings holds"); code != 0 {
		t.Fatalf("recording a block exited %d:\n%s", code, out)
	}

	code, out := run(t, luna, repo, ledgerPath, "state", "--run", "E2E-2")
	if code != 0 {
		t.Fatalf("state exited %d:\n%s", code, out)
	}
	for _, want := range []string{"blocked", "the question", "looked in", "what unblocks"} {
		if !strings.Contains(out, want) {
			t.Errorf("the state is missing %q:\n%s", want, out)
		}
	}

	code, out = run(t, luna, repo, ledgerPath, "report")
	if code != 0 {
		t.Fatalf("report exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "needs somebody") {
		t.Errorf("the report does not surface the blocked run:\n%s", out)
	}
}

// INV-4 through the binary: writing where a write would not survive refuses
// rather than reporting success.
func TestTheBinaryRefusesALedgerThatWouldNotSurvive(t *testing.T) {
	luna := build(t)
	repo, _ := workspace(t)

	inMemory, err := os.MkdirTemp("/dev/shm", "luna-e2e-")
	if err != nil {
		t.Skipf("no writable tmpfs to test against: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(inMemory) })

	code, out := run(t, luna, repo, inMemory,
		"record", "--run", "E2E-3", "--event", "phase", "--status", "running")
	if code == 0 {
		t.Fatal("a ledger in memory was written to; every line would be lost in silence")
	}
	if !strings.Contains(out, "rw-map") {
		t.Errorf("the refusal does not say how to fix it:\n%s", out)
	}
}

func build(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "luna")
	out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("building: %v: %s", err, out)
	}
	return binary
}

// workspace answers a repository and a ledger directory, both on durable storage.
//
// Not t.TempDir(): /tmp is tmpfs here and on any systemd default, and the
// durability guard refuses it — correctly.
func workspace(t *testing.T) (repo, ledgerDir string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	root, err := os.MkdirTemp(home, ".luna-e2e-")
	if err != nil {
		t.Skipf("cannot write under home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	if err := ledger.RequireDurable(filepath.Join(root, "probe")); err != nil {
		t.Skipf("home is not durable here: %v", err)
	}

	repo = filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--quiet", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "--quiet", "-m", "first"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	return repo, filepath.Join(root, "data")
}

func contractFile(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "contract.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// run executes the binary and answers its exit code and combined output.
func run(t *testing.T, luna, dir, dataHome string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(luna, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "XDG_DATA_HOME="+dataHome)

	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if err != nil && asExitError(err, &exitErr) {
		return exitErr.ExitCode(), string(out)
	}
	if err != nil {
		t.Fatalf("running luna %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return 0, string(out)
}

func asExitError(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError) //nolint:errorlint // the concrete type is what carries the exit code
	if ok {
		*target = e
	}
	return ok
}
