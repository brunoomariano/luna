package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EditInEditor opens content in the user's editor and returns what they left.
//
// It is the idiom of `git commit` and `crontab -e`: the file appears, they change
// it or they do not, and saving is the whole protocol. Nothing new to learn.
//
// Leaving without changing anything returns the content unchanged, and the caller
// reads that as "never mind" — an editor that exits clean is not consent.
func EditInEditor(current string) (string, error) {
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("VISUAL"))
	}
	if editor == "" {
		return "", errors.New("no $EDITOR or $VISUAL set; use approve or reject instead")
	}

	file, err := os.CreateTemp("", "luna-*.md")
	if err != nil {
		return "", fmt.Errorf("creating a scratch file: %w", err)
	}
	path := file.Name()
	defer func() { _ = os.Remove(path) }()

	if _, err := file.WriteString(current); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("writing to %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("closing %s: %w", path, err)
	}

	// $EDITOR routinely carries arguments — `code --wait`, `emacsclient -nw`, `vim
	// -f`. Passing the whole string as the program name would look for an
	// executable called "code --wait" and fail with a confusing error.
	parts := strings.Fields(editor)
	// A fresh slice rather than appending to parts[1:]: appending to a subslice
	// can write into the backing array it shares, which is a bug that only shows
	// up with the right capacity.
	args := make([]string, 0, len(parts))
	args = append(args, parts[1:]...)
	args = append(args, path)

	// The editor gets the real terminal: it is an interactive program, and piping
	// its input would leave the user typing into nothing.
	cmd := exec.Command(parts[0], args...) //nolint:gosec // the editor is the user's own choice
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s exited with an error: %w", filepath.Base(parts[0]), err)
	}

	edited, err := os.ReadFile(path) //nolint:gosec // the path is one we just created
	if err != nil {
		return "", fmt.Errorf("reading back %s: %w", path, err)
	}
	return string(edited), nil
}

// Editor returns the edit function to install in Env, or nil when no editor is
// configured. Returning nil rather than a function that always fails lets the
// command say "set $EDITOR" instead of launching nothing and reporting a crash.
func Editor() func(string) (string, error) {
	if strings.TrimSpace(os.Getenv("EDITOR")) == "" &&
		strings.TrimSpace(os.Getenv("VISUAL")) == "" {
		return nil
	}
	return EditInEditor
}
