package rex

import (
	sdk "github.com/helpin-ai/agent-runtime-go"
	"testing"
)

func TestSelectWorkerCapabilities(t *testing.T) {
	native := []string{"request_user_input", "read_file", "write_file"}
	servers := []sdk.RunMCPServer{{ServerName: "Helpin CRM", Tools: []sdk.RunMCPTool{{Name: "list", Access: "read"}, {Name: "upsert", Access: "write"}}}}
	selected := []string{"read_file", "mcp__Helpin_CRM__list"}
	allowed, narrowed, err := selectWorkerTools(native, servers, &selected)
	if err != nil || len(allowed) != 2 || len(narrowed) != 1 || len(narrowed[0].Tools) != 1 || narrowed[0].Tools[0].Name != "list" {
		t.Fatalf("selection: %v %v %v", allowed, narrowed, err)
	}
	if len(servers[0].Tools) != 2 {
		t.Fatal("parent capabilities mutated")
	}
	empty := []string{}
	allowed, narrowed, err = selectWorkerTools(native, servers, &empty)
	if err != nil || len(allowed) != 1 || len(narrowed) != 0 {
		t.Fatal("empty selection inherited tools")
	}
	bad := []string{"shell"}
	if _, _, err := selectWorkerTools(native, servers, &bad); err == nil {
		t.Fatal("ungranted tool accepted")
	}
	allowed, narrowed, err = selectWorkerTools(native, servers, nil)
	if err != nil || allowed != nil || len(narrowed[0].Tools) != 2 {
		t.Fatal("legacy inheritance changed")
	}
}
