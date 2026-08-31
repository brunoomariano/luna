package cli

import (
	"fmt"
	"os"

	"github.com/brunoomariano/luna/src/internal/agent"
	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/node"
)

// consoleCommand says where to watch a stage's agent, and how.
//
// Luna starts every agent headless. Nothing it says, nothing it reasons about and
// nothing Luna hands it appears anywhere while it runs: the process's output goes
// into a buffer that is read for a JSON reply and then dropped, and the only time
// any of it reaches a person is when the stage delivered nothing.
//
// The harness keeps its own transcript, written as the session goes, and that
// file is the console. This command names it. It does not read it, and that is
// deliberate: printing it would mean Luna parsing one harness's format, which is
// a coupling it does not have and a second thing to keep in step — while `tail`,
// `jq` and a herdr pane already read the file better than Luna would.
func consoleCommand(env Env, args []string) error {
	env, id, rest, err := taskFrom(env, args)
	if err != nil {
		return err
	}
	asJSON, err := wantsJSON(rest)
	if err != nil {
		return err
	}

	state, err := env.replay(id)
	if err != nil {
		return err
	}
	flow, err := env.flowOf(id)
	if err != nil {
		return err
	}

	consoles := consolesOf(state, flow)
	if asJSON {
		return writeJSON(env.Out, consoles)
	}
	printConsoles(env, id, state, consoles)
	return nil
}

// ConsoleReport is one stage's session and where to watch it.
type ConsoleReport struct {
	Stage        string `json:"stage"`
	Agent        string `json:"agent"`
	Session      string `json:"session"`
	Resume       string `json:"resume,omitempty"`
	FollowFilter string `json:"follow_filter,omitempty"`

	// Path is the transcript, empty when Luna does not know this harness's
	// layout. Live says only whether the file is there now; the task status says
	// whether its stage is running.
	Path string `json:"path,omitempty"`
	Live bool   `json:"live"`
}

// consolesOf pairs every stage that started an agent with the session it used.
//
// The session id has been in the log all along — it is what lets a later stage
// resume the same conversation — and nothing ever showed it. A person watching a
// run had the cost of every stage and no way to see what any of them did.
func consolesOf(state fsm.TaskState, flow []fsm.Stage) []ConsoleReport {
	reports := make([]ConsoleReport, 0, len(flow))
	for _, stage := range flow {
		// A stage that starts no agent has no console, whatever the log says. The
		// spend is keyed by stage rather than by agent, so a mechanical stage can
		// carry one — and reporting a session for something that never opened a
		// conversation would send somebody looking for a file nothing wrote.
		if stage.Mechanical() {
			continue
		}
		spend, ran := state.Spent[stage.ID]
		if !ran || spend.Session == "" {
			continue
		}

		kind := spend.Agent
		if kind == "" {
			kind = stage.Agent
		}
		report := ConsoleReport{
			Stage: string(stage.ID), Agent: kind, Session: spend.Session,
		}
		report.Resume, _ = agent.ResumeCommand(kind, spend.Session)
		report.FollowFilter, _ = agent.ConsoleFilter(kind)
		if worktree, err := node.WorktreePath(".", state.ID, string(stage.ID)); err == nil {
			if path, known := agent.ConsolePath(kind, worktree, spend.Session); known {
				report.Path = path
				if _, err := os.Stat(path); err == nil {
					report.Live = true
				}
			}
		}
		reports = append(reports, report)
	}
	return reports
}

func printConsoles(env Env, id string, state fsm.TaskState, consoles []ConsoleReport) {
	if len(consoles) == 0 {
		fmt.Fprintf(env.Out, "%s has started no agent yet — there is no session to watch\n", id)
		return
	}

	fmt.Fprintf(env.Out, "%s\n\n", id)
	var example ConsoleReport
	for _, one := range consoles {
		here := ""
		if currentStageIsRunning(state, one) {
			here = "  ← running"
		}
		fmt.Fprintf(env.Out, "  %-13s %s%s\n", one.Stage, one.Agent, here)
		printResumeCommand(env, state, one)
		if one.Path == "" {
			fmt.Fprintf(env.Out, "  %-13s session %s — Luna does not know where this harness "+
				"keeps its transcripts\n", "", one.Session)
			continue
		}
		fmt.Fprintf(env.Out, "  %-13s %s%s\n", "", one.Path, missingNote(one))
		if one.Path != "" && (example.Path == "" || one.Live) {
			example = one
		}
	}

	printHowToFollow(env, example)
}

func printResumeCommand(env Env, state fsm.TaskState, one ConsoleReport) {
	if one.Resume == "" {
		return
	}

	fmt.Fprintf(env.Out, "  %-13s `%s`\n", "", one.Resume)
	if currentStageIsRunning(state, one) {
		fmt.Fprintf(env.Out, "  %-13s warning: this is the stage's conversation, not a copy.\n", "")
		fmt.Fprintf(env.Out, "  %-13s Anything you type joins it, and Luna does not record that turn.\n", "")
		return
	}
	if stageHasEnded(state, one) {
		fmt.Fprintf(env.Out, "  %-13s stage has ended — safe to open\n", "")
	}
}

func currentStageIsRunning(state fsm.TaskState, one ConsoleReport) bool {
	return state.Status == fsm.StatusRunning && string(state.Stage) == one.Stage
}

func stageHasEnded(state fsm.TaskState, one ConsoleReport) bool {
	if string(state.Stage) != one.Stage {
		return true
	}
	return state.Status == fsm.StatusStageDone || state.IsTerminal()
}

func printHowToFollow(env Env, example ConsoleReport) {
	path := example.Path
	if path == "" {
		path = "<path>"
	}

	fmt.Fprintf(env.Out, "\nfollow it as it happens:\n")
	if example.FollowFilter == "" {
		fmt.Fprintf(env.Out, "  tail -f %s\n", path)
	} else {
		fmt.Fprintf(env.Out, "  tail -f %s \\\n    | jq -r '%s'\n", path, example.FollowFilter)
	}
	fmt.Fprintf(env.Out, "\nThe transcript is the harness's own, written as the session goes, so this works\n")
	fmt.Fprintf(env.Out, "in any pane — herdr, tmux, a second terminal. It carries what the agent said,\n")
	fmt.Fprintf(env.Out, "what it ran, and the prompt Luna handed it. It does not expose readable reasoning.\n")
}

// missingNote marks a transcript that is not on disk.
//
// Absent is ordinary rather than wrong: the file arrives when the session starts
// and stays after the worktree is removed, so a stage that has not run yet and
// one whose harness pruned its history look the same from here.
func missingNote(one ConsoleReport) string {
	if one.Live {
		return ""
	}
	return "  (not on disk)"
}
