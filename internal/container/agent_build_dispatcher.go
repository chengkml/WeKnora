package container

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// startAgentBuildDispatcher launches the agent build task dispatcher and its
// status poller.
//
// It mirrors recoverPendingWikiTasks: every provider and handler is registered
// first, then the background loops start, and the process lifetime is the
// lifecycle boundary (the loops exit with the process).
//
// The dispatcher is what turns the fire-and-forget hand-off of wiki builds to
// the external agent gateway into a durable, observable queue: it caps how many
// builds are in flight at the gateway at once, tracks each one to a terminal
// state, and backs off instead of dropping a task when the gateway is busy.
func startAgentBuildDispatcher(svc interfaces.AgentBuildTaskService) {
	if svc == nil {
		return
	}
	svc.Start(context.Background())
}
