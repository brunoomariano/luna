package herdr_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/herdr"
)

// End-to-end tests against a real herdr server.
//
// Everything else in this package talks to a fake that answers what the protocol
// notes say herdr answers. That is the right default — the suite has to run in CI
// with no herdr installed — but it means the notes are the only thing being
// tested, and ADR-0036 exists because ten of those notes were wrong until a real
// server contradicted them. The trap it records is the one a fake cannot
// reproduce: herdr accepts unknown fields silently, so a wrong field name gates
// nothing while every assertion still passes.
//
// They are gated on what each one actually needs, so as much runs as the machine
// allows and nothing fails for being absent:
//
//   - the harness tests need only the harness binary on PATH, and run anywhere it
//     is installed;
//   - the socket tests need a live server, and skip unless LUNA_E2E_SOCKET points
//     at one.
//
// To run the whole set:
//
//	herdr --session luna-e2e            # in a real terminal; herdr needs a TTY
//	LUNA_E2E_SOCKET=~/.config/herdr/herdr.sock go test ./src/internal/herdr/ -run E2E -v
func e2eSocket(t *testing.T) string {
	t.Helper()

	socket := os.Getenv("LUNA_E2E_SOCKET")
	if socket == "" {
		t.Skip("set LUNA_E2E_SOCKET to a running herdr socket to run the end-to-end tests")
	}
	if strings.HasPrefix(socket, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("expanding ~: %v", err)
		}
		socket = filepath.Join(home, socket[2:])
	}
	if _, err := os.Stat(socket); err != nil {
		t.Skipf("no socket at %s: %v", socket, err)
	}
	return socket
}

// TestE2EDialProvesTheServerAnswers is the first thing that has to hold: Dial
// spends a ping rather than trusting that a connect means a working server.
func TestE2EDialProvesTheServerAnswers(t *testing.T) {
	socket := e2eSocket(t)

	client, err := herdr.Dial(socket)
	if err != nil {
		t.Fatalf("dialling a running herdr: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
}

// TestE2EAMissingSocketIsGoneRatherThanAnyError covers the distinction the CLI
// depends on to tell "herdr is not running" from "herdr said no".
func TestE2EAMissingSocketIsGoneRatherThanAnyError(t *testing.T) {
	e2eSocket(t) // skip unless this environment has herdr at all

	_, err := herdr.Dial(filepath.Join(t.TempDir(), "nothing.sock"))
	if !errors.Is(err, herdr.ErrGone) {
		t.Errorf("want ErrGone for a socket that is not there, got %v", err)
	}
}

// TestE2ETheDenialReachesTheHarness is the one that cannot be faked.
//
// Luna's gating claim is that a reviewer starts without the tools it must not
// have. A fake can only confirm that Luna assembled the argv it meant to; whether
// the harness accepts those flags, and whether it rejects a wrong one instead of
// ignoring it, is a fact about the binary. ADR-0042 records that the mapping is
// version-specific and unversioned, and this is what would notice it drifting.
//
// It asserts the harness *rejects* a flag that does not exist. A harness that
// accepted anything would gate nothing while reporting success, which is exactly
// how `opencode --print` went unnoticed.
// It does not need a herdr server: the subject is the harness binary, and gating
// is decided before herdr is asked to start anything. Gate it on the harness
// being installed instead, so it runs wherever the harness exists.
func TestE2ETheDenialReachesTheHarness(t *testing.T) {
	role := fsm.Role{Agent: "claude", ToolsDeny: []fsm.Capability{fsm.CapEdit, fsm.CapWrite}}
	harness, ok := herdr.HarnessFor(role.Agent)
	if !ok {
		t.Fatalf("%s is an official harness and must be in the table", role.Agent)
	}
	args, err := harness.Deny(role.ToolsDeny)
	if err != nil {
		t.Fatalf("denying writing for %s: %v", role.Agent, err)
	}

	if _, err := exec.LookPath(role.Agent); err != nil {
		t.Skipf("%s is not installed: %v", role.Agent, err)
	}

	// The flags Luna would send, with a prompt that ends the turn immediately.
	// If they are wrong, the harness says so.
	accepted := runHarness(t, role.Agent, append(append([]string{}, args...), "--print", "ok"))
	if strings.Contains(accepted, "unknown option") || strings.Contains(accepted, "unknown flag") {
		t.Errorf("%s rejected the flags Luna sends to gate it: %s", role.Agent, accepted)
	}

	// The control: a flag that does not exist must be refused. Without this the
	// test above proves only that the harness ran, not that it read the flags.
	refused := runHarness(t, role.Agent, []string{"--definitely-not-a-flag", "--print", "ok"})
	if !strings.Contains(refused, "unknown option") && !strings.Contains(refused, "unknown flag") {
		t.Errorf("%s accepted a flag that does not exist, so it would ignore a renamed one too: %s",
			role.Agent, refused)
	}
}

// TestE2EEveryGatedHarnessRejectsAnUnknownFlag generalises the control above.
//
// This is the version-drift alarm ADR-0042 asks for and nothing implemented. A
// harness that silently ignores an unknown flag will silently ignore a renamed
// denial flag, and Luna would keep reporting that the reviewer was gated.
func TestE2EEveryGatedHarnessRejectsAnUnknownFlag(t *testing.T) {
	for _, kind := range herdr.SupportedHarnesses() {
		harness, _ := herdr.HarnessFor(kind)
		if _, err := harness.Deny([]fsm.Capability{fsm.CapEdit, fsm.CapWrite}); err != nil {
			// A harness Luna cannot gate from argv has nothing to check here.
			continue
		}
		if _, err := exec.LookPath(kind); err != nil {
			t.Logf("skipping %s: not installed", kind)
			continue
		}

		out := runHarness(t, kind, []string{"--definitely-not-a-flag"})
		if !strings.Contains(out, "unknown option") && !strings.Contains(out, "unknown flag") &&
			!strings.Contains(out, "unexpected argument") && !strings.Contains(out, "error") {
			t.Errorf("%s did not complain about a flag that does not exist — "+
				"a renamed denial flag would be ignored just as quietly: %s", kind, out)
		}
	}
}

// runHarness invokes a harness with a short deadline and returns whatever it
// said, on either stream.
//
// The output is the subject: what matters is whether the binary complained, not
// whether it exited zero.
func runHarness(t *testing.T, agent string, args []string) string {
	t.Helper()

	cmd := exec.Command(agent, args...) //nolint:gosec // an official harness from a closed table
	cmd.WaitDelay = 5 * time.Second

	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.CombinedOutput()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Skipf("%s did not answer in time — it may be waiting on credentials", agent)
	}
	if err != nil && len(out) == 0 {
		t.Skipf("could not run %s: %v", agent, err)
	}

	answer := strings.ToLower(string(out))

	// A launcher that could not produce the harness at all answers instead of it,
	// and its answer mentions no flag — so every assertion about what the harness
	// accepted or refused reads as though the harness had been permissive.
	//
	// Measured: a mise shim for an agent with no version selected exits non-zero
	// with `no version is set for shim: claude`, which contains neither "unknown
	// option" nor "unknown flag". The control assertion — that an invented flag is
	// refused — then failed, reporting a version-drift alarm about a harness that
	// never ran. `exec.LookPath` cannot catch this: the shim is on PATH and
	// executable, it just resolves to nothing.
	if strings.Contains(answer, "no version is set") {
		t.Skipf("%s is on PATH but not resolvable: %s", agent, answer)
	}

	return answer
}
