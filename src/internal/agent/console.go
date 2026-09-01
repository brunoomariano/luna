package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ConsolePath is where a harness leaves the transcript of one session.
//
// Luna starts every agent headless, so nothing of what it says, thinks or is
// asked appears anywhere while it runs — the process's own output goes into a
// buffer and is read for a JSON reply. The harness writes its own transcript
// though, incrementally, and that file is the console: following it is watching
// the stage happen.
//
// Derived rather than captured, and that is the choice. Luna could tee the
// process's output to a file of its own, and it would then own a second copy of
// something the harness already keeps, in a format it would have to keep in step
// with. Pointing at the original costs nothing and cannot drift.
//
// Answers false for a harness whose layout Luna does not know, for the same
// reason the table it reads is closed: a guessed path sends somebody to an empty
// file and lets them conclude the agent produced nothing.
func ConsolePath(kind, worktree, session string) (string, bool) {
	if session == "" || worktree == "" {
		return "", false
	}
	spec, known := harnesses[kind]
	if !known || spec.console == nil {
		return "", false
	}
	path := spec.console(worktree, session)
	return path, path != ""
}

// LiveSession is the session a stage is holding open in this worktree, right
// now, found by reading the harness's transcript directory rather than Luna's
// log.
//
// It exists because Luna cannot answer this from the log. A session id arrives
// in the harness's reply, so it is recorded when the stage *closes* — which
// leaves `luna console` able to describe every finished stage and not the one a
// person is actually watching. Reading the directory is the only source that
// exists while the stage is still running.
//
// Answers false for a harness whose transcripts are not filed by working
// directory. Codex names its by start time under a single sessions tree, so
// there is nothing to match a worktree against, and inventing one would point
// at another task's stage.
func LiveSession(kind, worktree string) (string, bool) {
	spec, known := harnesses[kind]
	if !known || spec.live == nil {
		return "", false
	}
	return spec.live(worktree)
}

// liveWindow is how stale a transcript may be and still count as the session
// happening now.
//
// It is bounded on both sides by something measured. A worktree's name is
// reused across attempts, so a rejected stage leaves its transcript in the very
// directory the next attempt writes to — taking the newest file with no window
// at all put a pane on a session that had already ended, three times in twelve
// seconds. And a running stage writes continuously: the gaps between writes are
// seconds, so a minute is already far past any of them.
const liveWindow = time.Minute

// claudeLive is the most recently written transcript in the directory Claude
// names after this worktree.
func claudeLive(worktree string) (string, bool) {
	dir := filepath.Dir(claudeConsole(worktree, "x"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}

	newest, found := time.Time{}, ""
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().After(newest) {
			continue
		}
		newest, found = info.ModTime(), entry.Name()
	}
	if found == "" || time.Since(newest) > liveWindow {
		return "", false
	}
	return strings.TrimSuffix(found, ".jsonl"), true
}

// ResumeCommand is the harness's interactive command for reopening a session.
func ResumeCommand(kind, session string) (string, bool) {
	spec, ok := harnesses[kind]
	if !ok || spec.resume == nil || session == "" {
		return "", false
	}
	return spec.resume(session), true
}

// ConsoleFilter is the jq program that renders one harness's transcript as a
// readable stream. Keeping it beside the transcript layout prevents a second
// Claude-only table from masquerading as generic observability.
func ConsoleFilter(kind string) (string, bool) {
	spec, ok := harnesses[kind]
	if !ok || spec.filter == "" {
		return "", false
	}
	return spec.filter, true
}

const claudeConsoleFilter = `if (.message.content|type)=="string" then "» " + .message.content
     else (.message.content[]?
       | if .type=="text" then .text
         elif .type=="tool_use" then "$ " + (.input.command // .name)
         else empty end)
     end`

const codexConsoleFilter = `if .type=="response_item" and .payload.type=="message" then
       (.payload.content[]?
        | if .type=="input_text" then "» " + .text
          elif .type=="output_text" then .text
          else empty end)
     elif .type=="event_msg" and .payload.type=="item_completed"
          and .payload.item.type=="CommandExecution" then
       "$ " + (.payload.item.command | join(" "))
     else empty end`

// claudeConsole is `~/.claude/projects/<cwd>/<session>.jsonl`, where the working
// directory is flattened by replacing every separator with a dash.
//
// Measured against the transcripts of a real run: the worktree
// `/tmp/.../scratchpad/real/wt-app-AVG-1-plan` becomes the directory
// `-tmp-...-scratchpad-real-wt-app-AVG-1-plan`, leading separator included.
func claudeConsole(worktree, session string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	flat := strings.ReplaceAll(worktree, string(filepath.Separator), "-")
	return filepath.Join(home, ".claude", "projects", flat, session+".jsonl")
}

// codexConsole is `$CODEX_HOME/sessions/YYYY/MM/DD/rollout-<local time>-<id>.jsonl`.
// The first twelve hexadecimal digits of Codex's UUIDv7 thread id are the Unix
// milliseconds used in both the id and filename, measured on codex-cli 0.151.0.
func codexConsole(_ string, session string) string {
	compact := strings.ReplaceAll(session, "-", "")
	if len(compact) < 12 {
		return ""
	}
	millis, err := strconv.ParseInt(compact[:12], 16, 64)
	if err != nil {
		return ""
	}

	home := os.Getenv("CODEX_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(userHome, ".codex")
	}
	started := time.UnixMilli(millis).In(localLocation())
	return filepath.Join(home, "sessions", started.Format("2006/01/02"),
		started.Format("rollout-2006-01-02T15-04-05-")+session+".jsonl")
}

func localLocation() *time.Location {
	if name := os.Getenv("TZ"); name != "" {
		if location, err := time.LoadLocation(name); err == nil {
			return location
		}
	}
	return time.Local
}
