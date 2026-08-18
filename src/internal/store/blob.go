package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

// MaxBlobSize is the ceiling on one artifact's content.
//
// A dense qa_report is around 30 KB, so this is roughly thirty times the real
// case and still refuses a build log pasted in by accident. The limit exists
// because the store holds facts and a blob holds content: without a ceiling, one
// stage's stray `make ci` output outweighs every task in the log.
const MaxBlobSize = 1 << 20 // 1 MiB

// ErrBlobTooLarge is content past MaxBlobSize.
var ErrBlobTooLarge = errors.New("the artifact is larger than the store accepts")

// ErrNoSuchBlob is a read for an artifact nothing has written.
var ErrNoSuchBlob = errors.New("no such artifact")

// Blob is one version of an artifact a stage handed over.
type Blob struct {
	TaskID   string
	Stage    string
	Artifact string

	// Seq is the log position this version was written at, which is what orders
	// two versions of the same artifact without a clock (ADR-0024).
	Seq int

	// Hash is the content's sha256, hex-encoded. It is what evidence names, so an
	// audit reads what satisfied a check rather than that something did.
	Hash string

	Body []byte
}

// PutBlob records one version of an artifact.
//
// It is an append: a second call for the same stage and artifact at a later seq
// adds a version rather than replacing one, and both stay readable (INV-core-2).
// Writing the same seq twice is a caller repeating itself, and the store refuses
// rather than quietly keeping one of them.
func (s *Store) PutBlob(b Blob) error {
	if err := s.mayWrite(); err != nil {
		return err
	}
	if len(b.Body) > MaxBlobSize {
		return fmt.Errorf("%w: %s from stage %q is %d bytes, and the ceiling is %d",
			ErrBlobTooLarge, b.Artifact, b.Stage, len(b.Body), MaxBlobSize)
	}

	sum := sha256.Sum256(b.Body)
	_, err := s.db.Exec(
		`INSERT INTO blobs (task_id, stage, artifact, seq, hash, body, at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		b.TaskID, b.Stage, b.Artifact, b.Seq, hex.EncodeToString(sum[:]), b.Body, s.now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("recording %s for %s at %d: %w", b.Artifact, b.TaskID, b.Seq, err)
	}
	return nil
}

// LatestBlob returns the most recent version of an artifact.
//
// The stage may be empty, and that is the ordinary read: `luna artifact get
// contract` wants whatever is current, whoever wrote it. Naming a stage asks what
// *that* stage handed over, which is the question an audit has.
func (s *Store) LatestBlob(taskID, stage, artifact string) (Blob, error) {
	query := `SELECT stage, seq, hash, body FROM blobs
	          WHERE task_id = ? AND artifact = ?`
	args := []any{taskID, artifact}
	if stage != "" {
		query += ` AND stage = ?`
		args = append(args, stage)
	}
	query += ` ORDER BY seq DESC LIMIT 1`

	b := Blob{TaskID: taskID, Artifact: artifact}
	err := s.db.QueryRow(query, args...).Scan(&b.Stage, &b.Seq, &b.Hash, &b.Body)
	if errors.Is(err, sql.ErrNoRows) {
		return Blob{}, fmt.Errorf("%w: %s has no %s%s", ErrNoSuchBlob, taskID, artifact, fromStage(stage))
	}
	if err != nil {
		return Blob{}, fmt.Errorf("reading %s for %s: %w", artifact, taskID, err)
	}
	return b, nil
}

// Blobs lists every version a task holds, oldest first.
//
// Bodies are left out: this answers "what is here and who wrote it", which is
// what `luna task show` and `luna artifact show` ask, and loading every version's
// content to print a list would be paying for what nobody reads.
func (s *Store) Blobs(taskID string) ([]Blob, error) {
	rows, err := s.db.Query(
		`SELECT stage, artifact, seq, hash, length(body) FROM blobs
		 WHERE task_id = ? ORDER BY seq, stage, artifact`, taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing what %s produced: %w", taskID, err)
	}
	defer func() { _ = rows.Close() }()

	var list []Blob
	for rows.Next() {
		b := Blob{TaskID: taskID}
		var size int
		if err := rows.Scan(&b.Stage, &b.Artifact, &b.Seq, &b.Hash, &size); err != nil {
			return nil, fmt.Errorf("reading what %s produced: %w", taskID, err)
		}
		// Body stays nil and the size travels in its place, so a caller can report
		// how big something is without the store having loaded it.
		b.Body = make([]byte, 0, size)
		list = append(list, b)
	}
	return list, rows.Err()
}

// ForgetBlobs removes every artifact a task produced.
//
// The one deletion the store allows, and it is not a rewrite of history: the log
// keeps every event, including which artifacts were produced and with what hash.
// What goes is the content, which is the part that grows.
func (s *Store) ForgetBlobs(taskID string) error {
	if err := s.mayWrite(); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM blobs WHERE task_id = ?`, taskID); err != nil {
		return fmt.Errorf("forgetting what %s produced: %w", taskID, err)
	}
	return nil
}

func fromStage(stage string) string {
	if stage == "" {
		return ""
	}
	return " from stage " + stage
}
