package cli

import (
	"fmt"
)

// ActiveTaskReport is one non-terminal task in `luna task list --json`.
type ActiveTaskReport struct {
	Project   string `json:"project"`
	ID        string `json:"id"`
	Status    string `json:"status"`
	Stage     string `json:"stage,omitempty"`
	Flow      string `json:"flow,omitempty"`
	Operation string `json:"operation,omitempty"`
}

// activeTasks is what is still going on: everything the log has not ended.
//
// Two ways a task is over, and the second is why this is not one condition. A
// task that replays says so itself. A task whose flow changed under it does not
// replay at all — abandoning is its one way out, and it appends without reading —
// so being ended is read off the log rather than off a state nothing can rebuild.
func activeTasks(tasks []globalTask) []ActiveTaskReport {
	report := make([]ActiveTaskReport, 0, len(tasks))
	for _, task := range tasks {
		if task.Ended || (task.Err == nil && task.State.IsTerminal()) {
			continue
		}
		line := ActiveTaskReport{Project: projectName(task.Project), ID: task.ID}
		if task.Err != nil {
			line.Status = "unreadable"
			report = append(report, line)
			continue
		}
		line.Status = string(task.State.Status)
		line.Stage = string(task.State.Stage)
		line.Flow = task.State.FlowName
		line.Operation = string(task.State.Operation())
		report = append(report, line)
	}
	return report
}

func taskList(env Env, args []string) error {
	asJSON, err := wantsJSON(args)
	if err != nil {
		return err
	}
	tasks, err := globalTasks(env)
	if err != nil {
		return err
	}

	report := activeTasks(tasks)

	if asJSON {
		return writeJSON(env.Out, report)
	}
	if len(report) == 0 {
		fmt.Fprintln(env.Out, "no active tasks")
		return nil
	}
	for _, task := range report {
		fmt.Fprintf(env.Out, "%-52s %-16s %-14s %-10s %s\n",
			task.Project, task.ID, task.Status, task.Stage, task.Flow)
	}
	return nil
}
