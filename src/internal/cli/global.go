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
}

// globalTasks replays every project from the central store. A changed flow is
// carried as data so watching commands can expose it; every other read failure
// aborts rather than producing a partial view that looks complete.
func globalTasks(env Env) ([]globalTask, error) {
	if env.GlobalStore == nil {
		return projectTasks(env.Store)
	}
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
			Project: ref.Project, ID: ref.ID, State: state, Err: err,
		})
	}
	return tasks, nil
}

func projectTasks(project *store.Store) ([]globalTask, error) {
	ids, err := project.Tasks()
	if err != nil {
		return nil, err
	}
	tasks := make([]globalTask, 0, len(ids))
	for _, id := range ids {
		state, err := project.ReplayOwnFlow(id)
		if err != nil && !errors.Is(err, store.ErrFlowChanged) {
			return nil, err
		}
		tasks = append(tasks, globalTask{
			Project: project.Project, ID: id, State: state, Err: err,
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
