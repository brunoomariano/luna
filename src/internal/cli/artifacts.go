package cli

import (
	"fmt"

	"github.com/brunoomariano/luna/src/internal/store"
)

// taskArtifacts adapts the store to what the socket server needs, binding it to
// one task and one log position.
//
// The seq is captured when the stage starts rather than read per write, because
// it is what orders two versions of the same artifact and the reducer is the only
// thing that moves it. A writer that asked the store for "now" would be
// reading a clock by another name.
type taskArtifacts struct {
	store  *store.Store
	taskID string
	seq    int
}

// NewTaskArtifacts binds a store to one task, for the duration of one stage.
func NewTaskArtifacts(s *store.Store, taskID string, seq int) *taskArtifacts { //nolint:revive // deliberately unexported
	return &taskArtifacts{store: s, taskID: taskID, seq: seq}
}

// PutArtifact records what a stage handed over.
//
// The stage comes from the server, never from the agent: an agent names the
// artifact and nothing else, so nothing it says can attribute work to another
// stage.
func (t *taskArtifacts) PutArtifact(stage, artifact string, body []byte) error {
	return t.store.PutBlob(store.Blob{
		TaskID: t.taskID, Stage: stage, Artifact: artifact, Seq: t.seq, Body: body,
	})
}

// GetArtifact reads what some stage handed over.
//
// An empty stage means "whatever is current", which is the ordinary read: an
// agent asking for the contract wants the contract, not an archaeology of who
// wrote which version.
func (t *taskArtifacts) GetArtifact(stage, artifact string) ([]byte, error) {
	blob, err := t.store.LatestBlob(t.taskID, stage, artifact)
	if err != nil {
		return nil, err
	}
	return blob.Body, nil
}

// artifactLine renders one stored artifact for a listing.
func artifactLine(b store.Blob) string {
	return fmt.Sprintf("%-16s %-12s %s %d bytes", b.Artifact, b.Stage, shortHash(b.Hash), cap(b.Body))
}

// shortHash abbreviates a digest for a line a person reads.
func shortHash(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}
