package cli

import (
	"errors"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/store"
)

type globalTask struct {
	Project string
	ID      string
	State   fsm.TaskState
	Err     error

	// Ended is whether somebody called this task off, for the case the replay
	// cannot answer. A task whose flow changed under it stops replaying, and
	// abandoning is the one way out — it appends without reading. The listings
	// read by replaying, so they went on calling an abandoned task unreadable and
	// the person who had just ended it watched it sit in the panel unchanged,
	// with nothing else to try.
	Ended bool
}

// endedInTheLog reads the last action rather than the state.
//
// It needs no flow, which is the point: whether somebody ended a task is a fact
// about its log, not about the contract it ran under — and the contract is
// exactly what is missing in the case this covers.
func endedInTheLog(project *store.Store, id string, replayed error) bool {
	if replayed == nil {
		return false
	}
	events, err := project.Events(id)
	if err != nil || len(events) == 0 {
		return false
	}
	return events[len(events)-1].Action == "Abandon"
}

// globalTasks replays every project from the central store. A changed flow is
// carried as data so watching commands can expose it; every other read failure
// aborts rather than producing a partial view that looks complete.
//
// There is no per-project fallback. There used to be one, for an Env with no
// global handle, and the binary has never built such an Env — one database holds
// every project, and the scoped view is a filter over the same handle.
func globalTasks(env Env) ([]globalTask, error) {
	refs, err := env.GlobalStore.TaskRefs()
	if err != nil {
		return nil, err
	}
	tasks := make([]globalTask, 0, len(refs))
	for _, ref := range refs {
		project := env.GlobalStore.ForProject(ref.Project)
		state, err := project.ReplayOwnFlow(ref.ID)
		if err != nil && !errors.Is(err, store.ErrFlowChanged) {
			return nil, err
		}
		tasks = append(tasks, globalTask{
			Project: ref.Project, ID: ref.ID, State: state,
			Err: err, Ended: endedInTheLog(project, ref.ID, err),
		})
	}
	return tasks, nil
}

func projectName(project string) string {
	if project == "" {
		return "current"
	}
	return project
}
