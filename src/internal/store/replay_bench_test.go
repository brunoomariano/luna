package store

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// BenchmarkReplay measures what it costs to rebuild a task's state from its log.
//
// The question it answers is whether replay belongs on the hot path. Every
// decision Luna takes starts by replaying — `luna next`, `luna run`'s loop, every
// gate answer — so if the cost grows with the log, a long task pays for its own
// history on every move.
//
// The sizes are chosen against what real runs produce. A twelve-stage task
// measured on the swarm bench lands at roughly 30 events; 100 is a task that
// looped a few times; 1000 and 5000 are there to show the shape rather than to
// describe anything that exists.
func BenchmarkReplay(b *testing.B) {
	for _, events := range []int{30, 100, 1000, 5000} {
		b.Run(fmt.Sprintf("%d-events", events), func(b *testing.B) {
			s := benchStore(b)
			seedLog(b, s, "LUNA-1", events)

			flow := fsm.DefaultFlow()
			b.ResetTimer()
			for range b.N {
				if _, err := s.Replay("LUNA-1", flow); err != nil {
					b.Fatalf("replaying: %v", err)
				}
			}
		})
	}
}

// BenchmarkEvents isolates the read from the reduce, so the two halves can be
// told apart: if the cost is in SQLite, a snapshot helps; if it is in Reduce, a
// snapshot helps more.
func BenchmarkEvents(b *testing.B) {
	for _, events := range []int{30, 1000} {
		b.Run(fmt.Sprintf("%d-events", events), func(b *testing.B) {
			s := benchStore(b)
			seedLog(b, s, "LUNA-1", events)

			b.ResetTimer()
			for range b.N {
				if _, err := s.Events("LUNA-1"); err != nil {
					b.Fatalf("reading: %v", err)
				}
			}
		})
	}
}

func benchStore(b *testing.B) *Store {
	b.Helper()

	s, err := OpenAs(filepath.Join(b.TempDir(), "luna.db"), LunaOwnsTheLog)
	if err != nil {
		b.Fatalf("opening the store: %v", err)
	}
	b.Cleanup(func() { _ = s.Close() })
	return s
}

// seedLog writes a log of roughly the requested length by looping a task through
// the flow: advance, complete, and a review finding when it runs out of stages.
//
// It is the shape a real log has rather than the same action repeated, because
// Reduce does different work per action and a log of one kind would measure the
// cheapest path.
func seedLog(b *testing.B, s *Store, id string, events int) {
	b.Helper()

	flow := fsm.DefaultFlow()
	if err := s.AppendAction(id, fsm.TaskCreated{Kind: fsm.KindFeature, Flow: fsm.Fingerprint(fsm.DefaultFlow())}); err != nil {
		b.Fatalf("creating: %v", err)
	}

	for range events {
		state, err := s.Replay(id, flow)
		if err != nil {
			b.Fatalf("replaying while seeding: %v", err)
		}

		action := nextSeedAction(state, flow)
		if action == nil {
			break
		}
		if err := s.AppendAction(id, action); err != nil {
			b.Fatalf("seeding: %v", err)
		}
	}
}

// nextSeedAction is whatever the state will accept next, so the seed walks a real
// path instead of asserting one.
func nextSeedAction(state fsm.TaskState, flow []fsm.Stage) fsm.Action {
	switch state.Status {
	case fsm.StatusAwaitingGate:
		return fsm.GateApprove{}

	case fsm.StatusRunning:
		stage := seedStage(flow, state.Stage)
		owed := append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...)
		evidence := map[fsm.Artifact]fsm.Evidence{}
		for _, a := range owed {
			evidence[a] = fsm.Evidence{
				Scope:   fsm.VerifierFor(stage, a).Proves(),
				Verdict: fsm.VerdictPassed,
			}
		}
		return fsm.Complete{Delivered: owed, Evidence: evidence, Flow: flow}

	default:
		// ready or stage_done: the next move is into a stage. A task that has run
		// out of stages loops instead, which is what makes a log long.
		if _, ok, _ := fsm.NextStage(flow, state.Stage, state.Context); !ok {
			return fsm.ReviewFinding{Aligned: true, Summary: "another round", Flow: flow}
		}
		return fsm.Advance{Flow: flow}
	}
}

func seedStage(flow []fsm.Stage, id fsm.StageID) fsm.Stage {
	for _, s := range flow {
		if s.ID == id {
			return s
		}
	}
	return fsm.Stage{ID: id}
}
