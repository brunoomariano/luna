package cli

import (
	"fmt"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// taskForget removes the content a finished task's stages handed over.
//
// This is the cleanup RFC-0008 promised: blobs are the one part of the store
// that grows with content rather than with facts, and a task that ended does not
// need its working documents any more. The log is untouched — every event stays,
// including each artifact's hash, so the history of what was produced outlives
// the content (INV-core-2).
//
// Only a terminal task may be forgotten. A running one is still handing
// artifacts to the stages ahead of it, and forgetting those mid-flight would
// block the next stage on a document that existed a moment ago — the person
// ends the task first (`luna task abandon`), then forgets it.
func taskForget(env Env, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: task forget needs exactly one id", ErrUsage)
	}
	id := args[0]

	state, err := env.Store.Replay(id, fsm.DefaultFlow())
	if err != nil {
		return err
	}
	if state.Seq == 0 {
		return fmt.Errorf("no task %q", id)
	}
	if !state.IsTerminal() {
		return fmt.Errorf("%s is %s — a task still holds its documents until it ends; "+
			"finish it, or end it with `luna task abandon %s`", id, state.Status, id)
	}

	blobs, err := env.Store.Blobs(id)
	if err != nil {
		return err
	}
	if len(blobs) == 0 {
		fmt.Fprintf(env.Out, "%s handed nothing over; there is nothing to forget\n", id)
		return nil
	}

	if err := env.Store.ForgetBlobs(id); err != nil {
		return err
	}

	// What was forgotten, by name: the person reading this may be about to
	// realise they wanted one of them, and "9 artifacts" gives them nothing to
	// check against the log's hashes.
	fmt.Fprintf(env.Out, "forgot what %s handed over (%d artifacts):\n", id, len(blobs))
	for _, blob := range blobs {
		fmt.Fprintf(env.Out, "  %s\n", artifactLine(blob))
	}
	fmt.Fprintf(env.Out, "the log keeps every event and hash; only the content is gone\n")
	return nil
}
