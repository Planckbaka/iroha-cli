package agent

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/tool"
)

var (
	ciWatchMu        sync.RWMutex
	ActiveCIWatchers = make(map[string]time.Time)
)

// ListActiveCIWatchers returns a copy of active CI watchers
func ListActiveCIWatchers() map[string]time.Time {
	ciWatchMu.RLock()
	defer ciWatchMu.RUnlock()
	res := make(map[string]time.Time)
	for k, v := range ActiveCIWatchers {
		res[k] = v
	}
	return res
}

type CIWatchArgs struct {
	Owner string `json:"owner" description:"The teammate name to notify upon failure (e.g., user-dev)"`
}

type CIWatchResult struct {
	Message string `json:"message" description:"Response message indicating watcher started"`
}

// AgentWatchCIHandler launches a background gh run watch process
func AgentWatchCIHandler(ctx tool.Context, args CIWatchArgs) (CIWatchResult, error) {
	if args.Owner == "" {
		return CIWatchResult{}, fmt.Errorf("owner is required")
	}

	ciWatchMu.Lock()
	ActiveCIWatchers[args.Owner] = time.Now()
	ciWatchMu.Unlock()

	go func() {
		defer func() {
			ciWatchMu.Lock()
			delete(ActiveCIWatchers, args.Owner)
			ciWatchMu.Unlock()
		}()

		// Watch the latest run for the current branch and exit with status code
		cmd := exec.Command("gh", "run", "watch", "--exit-status")
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out

		err := cmd.Run()
		if err != nil {
			output := out.String()
			lines := strings.Split(output, "\n")
			tail := lines
			if len(lines) > 20 {
				tail = lines[len(lines)-20:]
			}

			msgContent := fmt.Sprintf("CI Watcher Alert! The CI run failed.\n\nLogs snippet:\n%s\n\nPlease check the code and autofix.", strings.Join(tail, "\n"))

			_ = GlobalTeamManager.AppendToInbox(args.Owner, TeamMessage{
				Sender:    "CI-Watcher",
				Timestamp: float64(time.Now().Unix()),
				Content:   msgContent,
			})
		} else {
			_ = GlobalTeamManager.AppendToInbox(args.Owner, TeamMessage{
				Sender:    "CI-Watcher",
				Timestamp: float64(time.Now().Unix()),
				Content:   "CI Watcher Alert! The CI run completed successfully. You can merge now if automerge is not enabled.",
			})
		}
	}()

	return CIWatchResult{
		Message: "CI Watcher has been started in the background. You will receive an Inbox message when it completes.",
	}, nil
}

func registerCITools(r *ToolRegistry) {
	register(r, "agent_watch_ci", "Start a background process to monitor GitHub Actions CI status and send inbox notifications on failures.", AgentWatchCIHandler)
}
