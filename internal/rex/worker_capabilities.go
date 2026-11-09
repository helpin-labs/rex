package rex

import (
	"fmt"
	sdk "github.com/helpin-ai/agent-runtime-go"
	"regexp"
	"strings"
)

var workerToolComponent = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func selectWorkerTools(native []string, servers []sdk.RunMCPServer, requested *[]string) ([]string, []sdk.RunMCPServer, error) {
	if requested == nil {
		return nil, servers, nil
	}
	available := map[string]bool{}
	for _, name := range native {
		available[name] = true
	}
	alias := func(server, name string) string {
		clean := func(value string) string {
			return strings.Trim(workerToolComponent.ReplaceAllString(strings.TrimSpace(value), "_"), "_")
		}
		return "mcp__" + clean(server) + "__" + clean(name)
	}
	for _, server := range servers {
		for _, tool := range server.Tools {
			available[alias(server.ServerName, tool.Name)] = true
		}
	}
	selected := map[string]bool{}
	for _, name := range *requested {
		if !available[name] {
			return nil, nil, fmt.Errorf("worker tool %q is not available to this bot", name)
		}
		selected[name] = true
	}
	// A nonempty native list prevents the runtime's legacy empty-list inheritance.
	selected["request_user_input"] = true
	var allowed []string
	for _, name := range native {
		if selected[name] {
			allowed = append(allowed, name)
		}
	}
	if len(allowed) == 0 {
		return nil, nil, fmt.Errorf("bot is missing the worker request_user_input control")
	}
	narrowed := []sdk.RunMCPServer{}
	for _, server := range servers {
		server.Tools = append([]sdk.RunMCPTool(nil), server.Tools...)
		kept := server.Tools[:0]
		for _, tool := range server.Tools {
			if selected[alias(server.ServerName, tool.Name)] {
				kept = append(kept, tool)
			}
		}
		server.Tools = kept
		if len(kept) > 0 {
			narrowed = append(narrowed, server)
		}
	}
	return allowed, narrowed, nil
}
