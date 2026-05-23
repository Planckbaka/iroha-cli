package agent

import (
	"google.golang.org/adk/tool"
)

type MCPServerListArgs struct{}

type MCPServerListResult struct {
	Servers map[string]string `json:"servers"`
}

func MCPServerListHandler(ctx tool.Context, args MCPServerListArgs) (MCPServerListResult, error) {
	list := GlobalMCPRouter.ListServers()
	return MCPServerListResult{Servers: list}, nil
}
