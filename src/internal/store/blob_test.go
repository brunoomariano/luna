package store_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/store"
)

func blobStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.OpenAs(filepath.Join(t.TempDir(), "luna.db"), store.LunaOwnsTheLog)
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestAnArtifactIsReadBackAsItWasWritten is the floor: content in, same content
// out, with the hash the store computed.
func TestAnArtifactIsReadBackAsItWasWritten(t *testing.T) {
	s := blobStore(t)
	body := []byte("the contract, in full")

	if err := s.PutBlob(store.Blob{
		TaskID: "LUNA-1", Stage: "spec", Artifact: "contract", Seq: 4, Body: body,
	}); err != nil {
		t.Fatalf("recording the artifact: %v", err)
	}

	got, err := s.LatestBlob("LUNA-1", "", "contract")
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	if !bytes.Equal(got.Body, body) {
		t.Errorf("body: got %q, want %q", got.Body, body)
	}
	if got.Stage != "spec" {
		t.Errorf("stage: got %q, want %q", got.Stage, "spec")
	}
	// sha256 of the body above, so the store is computing rather than storing what
	// it was handed.
	if len(got.Hash) != 64 {
		t.Errorf("hash: got %q, want a 64-character sha256", got.Hash)
	}
}

// TestARevisionIsAppendedNotReplaced is INV-2 applied to content: the older
// version stays readable, and the newer one is what a plain read returns.
func TestARevisionIsAppendedNotReplaced(t *testing.T) {
	s := blobStore(t)
	for _, v := range []struct {
		seq  int
		body string
	}{{4, "first draft"}, {9, "after the gate"}} {
		if err := s.PutBlob(store.Blob{
			TaskID: "LUNA-1", Stage: "spec", Artifact: "contract", Seq: v.seq, Body: []byte(v.body),
		}); err != nil {
			t.Fatalf("recording seq %d: %v", v.seq, err)
		}
	}

	got, err := s.LatestBlob("LUNA-1", "", "contract")
	if err != nil {
		t.Fatalf("reading the current version: %v", err)
	}
	if string(got.Body) != "after the gate" {
		t.Errorf("a read must return the newest version, got %q", got.Body)
	}

	versions, err := s.Blobs("LUNA-1")
	if err != nil {
		t.Fatalf("listing versions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("both versions must survive, got %d", len(versions))
	}
}

// TestTwoStagesProducingOneArtifactAreToldApart is the reason the key carries the
// stage. `build` and `refactor` both produce `code`, and an audit asking what
// `build` handed over must not be given `refactor`'s answer.
func TestTwoStagesProducingOneArtifactAreToldApart(t *testing.T) {
	s := blobStore(t)
	for _, v := range []struct {
		stage, body string
		seq         int
	}{
		{"build", "what build wrote", 10},
		{"refactor", "what refactor rewrote", 20},
	} {
		if err := s.PutBlob(store.Blob{
			TaskID: "LUNA-1", Stage: v.stage, Artifact: "code", Seq: v.seq, Body: []byte(v.body),
		}); err != nil {
			t.Fatalf("recording %s: %v", v.stage, err)
		}
	}

	fromBuild, err := s.LatestBlob("LUNA-1", "build", "code")
	if err != nil {
		t.Fatalf("reading build's version: %v", err)
	}
	if string(fromBuild.Body) != "what build wrote" {
		t.Errorf("naming a stage must return that stage's version, got %q", fromBuild.Body)
	}

	// And with no stage named, the most recent from any stage.
	current, err := s.LatestBlob("LUNA-1", "", "code")
	if err != nil {
		t.Fatalf("reading the current version: %v", err)
	}
	if string(current.Body) != "what refactor rewrote" {
		t.Errorf("an unqualified read must return the newest, got %q", current.Body)
	}
}

// TestContentPastTheCeilingIsRefused pins the limit, and that the refusal names
// both numbers — a message that says neither what arrived nor what was wanted
// costs a debugging session.
func TestContentPastTheCeilingIsRefused(t *testing.T) {
	s := blobStore(t)

	err := s.PutBlob(store.Blob{
		TaskID: "LUNA-1", Stage: "qa", Artifact: "qa_report", Seq: 1,
		Body: bytes.Repeat([]byte("x"), store.MaxBlobSize+1),
	})
	if !errors.Is(err, store.ErrBlobTooLarge) {
		t.Fatalf("content past the ceiling must be refused, got %v", err)
	}
	if !strings.Contains(err.Error(), "1048577") || !strings.Contains(err.Error(), "1048576") {
		t.Errorf("the refusal must name the size and the ceiling, got %q", err)
	}
}

// TestContentAtTheCeilingIsAccepted pins the boundary from the other side, so the
// limit is not quietly off by one.
func TestContentAtTheCeilingIsAccepted(t *testing.T) {
	s := blobStore(t)

	if err := s.PutBlob(store.Blob{
		TaskID: "LUNA-1", Stage: "qa", Artifact: "qa_report", Seq: 1,
		Body: bytes.Repeat([]byte("x"), store.MaxBlobSize),
	}); err != nil {
		t.Fatalf("content exactly at the ceiling must be accepted, got %v", err)
	}
}

// TestAMissingArtifactSaysWhatWasLookedFor covers the read that finds nothing.
func TestAMissingArtifactSaysWhatWasLookedFor(t *testing.T) {
	s := blobStore(t)

	_, err := s.LatestBlob("LUNA-1", "spec", "contract")
	if !errors.Is(err, store.ErrNoSuchBlob) {
		t.Fatalf("a missing artifact must say so, got %v", err)
	}
	if !strings.Contains(err.Error(), "contract") || !strings.Contains(err.Error(), "spec") {
		t.Errorf("the error must name the artifact and the stage, got %q", err)
	}
}

// TestAReadOnlyStoreCannotWriteBlobs pins that the ownership rule covers content
// as well as events.
func TestAReadOnlyStoreCannotWriteBlobs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "luna.db")
	writer, err := store.OpenAs(path, store.LunaOwnsTheLog)
	if err != nil {
		t.Fatalf("opening for writing: %v", err)
	}
	_ = writer.Close()

	reader, err := store.Open(path)
	if err != nil {
		t.Fatalf("opening for reading: %v", err)
	}
	defer func() { _ = reader.Close() }()

	if err := reader.PutBlob(store.Blob{
		TaskID: "LUNA-1", Stage: "spec", Artifact: "contract", Seq: 1, Body: []byte("x"),
	}); !errors.Is(err, store.ErrNotTheOwner) {
		t.Errorf("a read-only store must refuse to write content, got %v", err)
	}
	if err := reader.ForgetBlobs("LUNA-1"); !errors.Is(err, store.ErrNotTheOwner) {
		t.Errorf("a read-only store must refuse to forget content, got %v", err)
	}
}

// TestForgettingATaskRemovesOnlyItsContent covers the cleanup, and that it is
// scoped: another task's artifacts are not collateral.
func TestForgettingATaskRemovesOnlyItsContent(t *testing.T) {
	s := blobStore(t)
	for _, id := range []string{"LUNA-1", "LUNA-2"} {
		if err := s.PutBlob(store.Blob{
			TaskID: id, Stage: "spec", Artifact: "contract", Seq: 1, Body: []byte("x"),
		}); err != nil {
			t.Fatalf("recording for %s: %v", id, err)
		}
	}

	if err := s.ForgetBlobs("LUNA-1"); err != nil {
		t.Fatalf("forgetting: %v", err)
	}

	if _, err := s.LatestBlob("LUNA-1", "", "contract"); !errors.Is(err, store.ErrNoSuchBlob) {
		t.Errorf("the forgotten task must have no content, got %v", err)
	}
	if _, err := s.LatestBlob("LUNA-2", "", "contract"); err != nil {
		t.Errorf("another task's content must survive, got %v", err)
	}
}

// TestBlobCallsOnAClosedStoreReportTheFault covers the database-error half of
// every blob operation: the store is gone, and each call says which read or
// write it was trying to do.
func TestBlobCallsOnAClosedStoreReportTheFault(t *testing.T) {
	s := blobStore(t)
	if err := s.PutBlob(store.Blob{
		TaskID: "LUNA-1", Stage: "spec", Artifact: "contract", Seq: 1, Body: []byte("x"),
	}); err != nil {
		t.Fatalf("recording before the close: %v", err)
	}
	_ = s.Close()

	if err := s.PutBlob(store.Blob{
		TaskID: "LUNA-1", Stage: "spec", Artifact: "contract", Seq: 2, Body: []byte("y"),
	}); err == nil || !strings.Contains(err.Error(), "recording") {
		t.Errorf("a write to a closed store names the write, got %v", err)
	}
	if _, err := s.LatestBlob("LUNA-1", "", "contract"); err == nil || !strings.Contains(err.Error(), "reading") {
		t.Errorf("a read from a closed store names the read, got %v", err)
	}
	if _, err := s.Blobs("LUNA-1"); err == nil || !strings.Contains(err.Error(), "listing") {
		t.Errorf("a listing from a closed store names the listing, got %v", err)
	}
	if err := s.ForgetBlobs("LUNA-1"); err == nil || !strings.Contains(err.Error(), "forgetting") {
		t.Errorf("a cleanup on a closed store names the cleanup, got %v", err)
	}
}
