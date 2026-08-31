package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

// GlobalScope is the scope a setting has when it belongs to the machine rather
// than to any one project.
//
// The empty string rather than a name, because it is also what a row written
// before scoping existed would carry — so the fallback and the default agree
// instead of needing a rule to reconcile them.
const GlobalScope = ""

// ErrNoSuchSetting is a read for a key nothing has set, at any scope.
var ErrNoSuchSetting = errors.New("no such setting")

// Setting is one recorded value of one key.
type Setting struct {
	Scope string
	Key   string

	// Seq orders two values of the same key without a clock, the way a blob's
	// does. It is the store's own counter rather than a task's: settings outlive
	// every task, so there is no log position to borrow.
	Seq int

	Value string

	// At is when the row was written, in unix seconds. Metadata, like the log's:
	// nothing reads it to decide a value, and `luna config --history` reads it to
	// show a person when somebody changed their mind.
	At int64
}

// PutSetting records a value for a key at a scope.
//
// An append, never an update (INV-2): the new value is a new row at a higher
// seq, and every earlier one stays readable. Unsetting writes an empty value
// rather than deleting, so "this project deliberately falls back to the global"
// and "nobody ever said" stay different facts.
func (s *Store) PutSetting(scope, key, value string) error {
	if err := s.mayWrite(); err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("a setting needs a key, and %q is not one", key)
	}

	next, err := s.nextSettingSeq(scope, key)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(
		`INSERT INTO settings (scope, key, seq, value, at) VALUES (?, ?, ?, ?, ?)`,
		scope, key, next, value, s.now().Unix(),
	); err != nil {
		return fmt.Errorf("recording %s at scope %q: %w", key, scope, err)
	}
	return nil
}

// nextSettingSeq is one past the highest seq this key has at this scope.
//
// Read and write in one process, which is safe for exactly the reason the log is:
// the daemon is the only writer, so there is no second process to race with.
func (s *Store) nextSettingSeq(scope, key string) (int, error) {
	var highest sql.NullInt64
	if err := s.db.QueryRow(
		`SELECT MAX(seq) FROM settings WHERE scope = ? AND key = ?`, scope, key,
	).Scan(&highest); err != nil {
		return 0, fmt.Errorf("reading the history of %s at scope %q: %w", key, scope, err)
	}
	if !highest.Valid {
		return 0, nil
	}
	return int(highest.Int64) + 1, nil
}

// Settings is what is configured at one scope: the current value of every key,
// which is the one at the highest seq.
//
// A key whose current value is empty is left out. That is what unsetting means
// here — the row stays for the audit, and the reader sees the key as unset.
func (s *Store) Settings(scope string) (map[string]string, error) {
	rows, err := s.db.Query(
		`SELECT key, value FROM settings
		 WHERE scope = ? AND seq = (
		     SELECT MAX(seq) FROM settings AS later WHERE later.scope = settings.scope AND later.key = settings.key
		 )`, scope)
	if err != nil {
		return nil, fmt.Errorf("reading the settings at scope %q: %w", scope, err)
	}
	defer func() { _ = rows.Close() }()

	current := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("reading a setting at scope %q: %w", scope, err)
		}
		if value != "" {
			current[key] = value
		}
	}
	return current, rows.Err()
}

// SettingHistory is every value a key has held at a scope, oldest first.
//
// The reason the table is append-only rather than a lookup: a setting is a
// decision, and the file this replaced recorded who changed it in git.
func (s *Store) SettingHistory(scope, key string) ([]Setting, error) {
	rows, err := s.db.Query(
		`SELECT seq, value, at FROM settings WHERE scope = ? AND key = ? ORDER BY seq`, scope, key)
	if err != nil {
		return nil, fmt.Errorf("reading the history of %s at scope %q: %w", key, scope, err)
	}
	defer func() { _ = rows.Close() }()

	var history []Setting
	for rows.Next() {
		one := Setting{Scope: scope, Key: key}
		if err := rows.Scan(&one.Seq, &one.Value, &one.At); err != nil {
			return nil, fmt.Errorf("reading a value of %s: %w", key, err)
		}
		history = append(history, one)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(history) == 0 {
		return nil, fmt.Errorf("%w: nothing has set %s at scope %q", ErrNoSuchSetting, key, scope)
	}
	return history, nil
}

// SettingScopes is every scope that has ever had a setting, sorted.
//
// It exists so `luna config` can show what the machine holds without being told
// which projects to ask about.
func (s *Store) SettingScopes() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT scope FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("reading the configured scopes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var scopes []string
	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err != nil {
			return nil, fmt.Errorf("reading a configured scope: %w", err)
		}
		scopes = append(scopes, scope)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(scopes)
	return scopes, nil
}
