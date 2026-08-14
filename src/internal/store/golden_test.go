package store

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// Golden logs: real payloads, checked into the repository, replayed by this build.
//
// The suite's other tests encode and decode in the same process, which proves the
// codec is symmetric and nothing else. A log written by yesterday's build is the
// case that matters, and no in-process test can produce one — so the payloads
// live on disk and the code moves out from under them.
//
// The practice is Temporal's, whose replayer runs a corpus of recorded histories
// against current code in CI. Theirs is captured from a running server; Luna's is
// written by the test, because every action is a plain struct and the whole point
// is that the bytes stay put while the code changes.
//
// Regenerate with `go test ./src/internal/store/ -run Golden -update`, and read
// the diff rather than trusting it: a changed byte is a changed log format, and
// the question is always whether logs already written can still be read.

// goldenDir is where the recorded payloads live, beside the test that reads them.
const goldenDir = "testdata/golden"

// update re-records the corpus instead of checking against it. Off by default:
// a corpus that regenerates itself on failure proves nothing.
var update = flag.Bool("update", false, "re-record the golden payloads")

// TestGoldenPayloadsStillDecode is the compatibility check.
//
// Every action, with every field populated, encoded once and read forever after.
// A rename that JSON tags would have absorbed shows up here as a field that came
// back empty; a rename they do not absorb shows up as a decode error.
func TestGoldenPayloadsStillDecode(t *testing.T) {
	for _, c := range goldenCases() {
		path := filepath.Join(goldenDir, c.name+".json")

		if *update {
			write(t, path, c.action)
			continue
		}

		recorded, err := os.ReadFile(path) //nolint:gosec // a path this test built
		if err != nil {
			t.Errorf("%s: %v — run with -update to record it", c.name, err)
			continue
		}

		event := Event{Action: c.recordedAs, Payload: strings.TrimSpace(string(recorded))}
		got, err := decodeAction(event, fsm.DefaultFlow())
		if err != nil {
			t.Errorf("%s no longer decodes: %v\nrecorded payload: %s", c.name, err, recorded)
			continue
		}

		// Re-encoding has to produce the same bytes. Decoding successfully is not
		// enough: a field silently dropped decodes fine and comes back missing,
		// which is exactly the failure that has no error attached to it.
		_, payload, err := encodeAction(got)
		if err != nil {
			t.Errorf("%s: re-encoding failed: %v", c.name, err)
			continue
		}
		if payload != strings.TrimSpace(string(recorded)) {
			t.Errorf("%s: the payload changed shape.\n  recorded: %s\n  now:      %s\n"+
				"A log already written cannot be read the way it was. If that is intended, "+
				"say so in an ADR and re-record with -update.", c.name, recorded, payload)
		}
	}
}

// TestGoldenLogsReplayToTheSameState is the whole property in one test.
//
// A recorded sequence of events, replayed by this build, has to land the task
// where it landed when the log was written. This is what "the log is the state"
// means, and it is the claim every command depends on.
func TestGoldenLogsReplayToTheSameState(t *testing.T) {
	path := filepath.Join(goldenDir, "replay.json")

	if *update {
		writeReplayCorpus(t, path)
		return
	}

	var recorded struct {
		Events []Event `json:"events"`
		Ends   struct {
			Stage  string `json:"stage"`
			Status string `json:"status"`
			Seq    int    `json:"seq"`
		} `json:"ends"`
	}
	raw, err := os.ReadFile(path) //nolint:gosec // a path this test built
	if err != nil {
		t.Fatalf("%v — run with -update to record it", err)
	}
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatalf("reading the corpus: %v", err)
	}

	s := openTemp(t)
	for _, e := range recorded.Events {
		if err := s.Append("LUNA-1", e); err != nil {
			t.Fatalf("seeding seq %d: %v", e.Seq, err)
		}
	}

	state, err := s.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		// The flow's fingerprint is part of the recorded log, so this fires when
		// the shipped flow changed — which is ADR-0046 working, and the corpus
		// showing what it costs.
		if errors.Is(err, ErrFlowChanged) {
			t.Fatalf("the shipped flow changed, so a log written under the old one no longer "+
				"replays: %v\nThat is ADR-0046 doing its job. Re-record with -update once "+
				"the change is deliberate.", err)
		}
		t.Fatalf("a recorded log must still replay: %v", err)
	}

	if string(state.Stage) != recorded.Ends.Stage || string(state.Status) != recorded.Ends.Status {
		t.Errorf("the same log replays somewhere else now.\n  then: stage=%q status=%q\n"+
			"  now:  stage=%q status=%q",
			recorded.Ends.Stage, recorded.Ends.Status, state.Stage, state.Status)
	}
	if state.Seq != recorded.Ends.Seq {
		t.Errorf("the log position moved: recorded %d, replayed %d", recorded.Ends.Seq, state.Seq)
	}
}

// goldenCases is one of every action, with every field carrying a value.
//
// Zero values would hide exactly what this test is for: a field that stops being
// written looks identical to one that was never set.
func goldenCases() []struct {
	name       string
	recordedAs string
	action     fsm.Action
} {
	return []struct {
		name       string
		recordedAs string
		action     fsm.Action
	}{
		{"task-created", actionTaskCreated, fsm.TaskCreated{
			Kind: fsm.KindFeature, Profile: fsm.ProfileTurbo, Flow: "c0c9ff4d121b43fc",
		}},
		{"advance", actionAdvance, fsm.Advance{GateDecision: fsm.GateDecisionWaited}},
		{"complete", actionComplete, fsm.Complete{
			Delivered: []fsm.Artifact{"code", "tests_green"},
			Evidence: map[fsm.Artifact]fsm.Evidence{"tests_green": {
				Scope: fsm.ScopeTargeted, Verdict: fsm.VerdictPassed,
				Command: "make test", ExitCode: 0, Detail: "12 passed", RecordedAt: 7,
			}},
		}},
		{"fail", actionFail, fsm.Fail{Reason: "the node died"}},
		{"gate-adjust", actionGateAdjust, fsm.GateAdjust{Payload: "the contract a human fixed"}},
		{"gate-reject", actionGateReject, fsm.GateReject{Reason: "the scenarios miss a case"}},
		{"review-finding", actionReviewFinding, fsm.ReviewFinding{
			Aligned: true, Summary: "the error path is unhandled",
			Limits:       fsm.LoopLimits{MaxRounds: 3, NoProgress: 2, Oscillation: 2},
			GateDecision: fsm.GateDecisionPassed,
		}},
		{"block", actionBlock, fsm.Block{Reason: "retries exhausted"}},
		{"abandon", actionAbandon, fsm.Abandon{Reason: "superseded by LUNA-2"}},
	}
}

// write records one payload.
func write(t *testing.T, path string, action fsm.Action) {
	t.Helper()

	_, payload, err := encodeAction(action)
	if err != nil {
		t.Fatalf("encoding %T: %v", action, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(payload+"\n"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// writeReplayCorpus records a task's whole log, plus where it ended up.
func writeReplayCorpus(t *testing.T, path string) {
	t.Helper()

	s := openTemp(t)
	for _, action := range []fsm.Action{
		fsm.TaskCreated{
			Kind: fsm.KindChore, Profile: fsm.ProfileTurbo,
			Flow: fsm.Fingerprint(fsm.DefaultFlow()),
		},
		fsm.Advance{Flow: fsm.DefaultFlow()},
		fsm.Complete{
			// `setup` is the first stage since ADR-0062 removed `commit` and
			// `discovery` went with it.
			Delivered: []fsm.Artifact{"worktree"},
			Evidence:  map[fsm.Artifact]fsm.Evidence{"worktree": fsm.Exists(2)},
			Flow:      fsm.DefaultFlow(),
		},
		fsm.Advance{Flow: fsm.DefaultFlow()},
	} {
		if err := s.AppendAction("LUNA-1", action); err != nil {
			t.Fatalf("recording %T: %v", action, err)
		}
	}

	events, err := s.Events("LUNA-1")
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	state, err := s.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}

	corpus := map[string]any{
		"events": events,
		"ends": map[string]any{
			"stage": string(state.Stage), "status": string(state.Status), "seq": state.Seq,
		},
	}
	body, err := json.MarshalIndent(corpus, "", "  ")
	if err != nil {
		t.Fatalf("encoding the corpus: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
