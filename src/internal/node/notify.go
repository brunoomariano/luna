package node

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// notifyTimeout bounds the notification call. It is a local process that draws a
// banner; a few seconds is generous, and a notifier that hangs must not hang the
// task it is reporting about.
const notifyTimeout = 10 * time.Second

// Notifier tells a person a task needs them.
//
// INV-5 says every task ends in a commit, a gate or a **notified** block, and
// the notification was the missing third: a block printed to stdout at 3am is a
// block nobody sees, which is the silent failure the invariant names.
//
// It delegates rather than implements. A terminal multiplexer already owns a
// notification layer and is already the process a person is looking at, so
// asking it to draw a banner is a line of code where a mail transport or a
// webhook would be a subsystem. Which channel to add beyond it stays open until
// real use says which one serves.
type Notifier struct {
	// Run sends the notification. A variable so a test can observe the call
	// without the external notifier on the machine.
	Run func(ctx context.Context, title, body string) error
}

// NewNotifier returns one that shells out to `herdr`, an external terminal
// multiplexer Luna does not depend on for anything else — this is a banner, not
// a transport the engine runs through.
//
// `herdr notification show` rather than a library: the CLI is the documented
// surface for this, and the arguments were checked against the binary — the
// discipline adopted after ten protocol facts turned out to be wrong.
//
// A machine without it is not an error worth failing a run over. The block is
// the fact worth keeping; the banner is only how it was announced.
func NewNotifier() Notifier {
	return Notifier{Run: func(ctx context.Context, title, body string) error {
		ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
		defer cancel()

		cmd := exec.CommandContext(ctx, "herdr", "notification", "show", title, "--body", body) //nolint:gosec // fixed subcommand
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("herdr notification show: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}}
}

// Blocked reports that a task stopped and needs a person.
//
// It returns an error rather than swallowing one, and the caller decides — but the
// caller's decision is always to carry on: a notification that failed must not
// turn a blocked task into a failed run, because the block is the fact worth
// keeping and the banner is how it was announced.
func (n Notifier) Blocked(ctx context.Context, taskID, reason string) error {
	if n.Run == nil {
		return nil
	}
	return n.Run(ctx, fmt.Sprintf("%s is blocked", taskID), reason)
}
