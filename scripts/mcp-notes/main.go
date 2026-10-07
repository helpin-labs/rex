// Command mcp-notes is a tiny Streamable HTTP MCP server used by the
// end-to-end checks: list_notes is a read tool, add_note a write tool.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"strings"
	"sync"

	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18930", "listen address")
	token := flag.String("token", "", "required bearer token")
	flag.Parse()
	var mu sync.Mutex
	notes := []string{"Dentist appointment on Friday at 10:00", "Buy oat milk"}
	result := func(value any) *protocol.CallToolResult {
		payload, _ := json.Marshal(value)
		return &protocol.CallToolResult{Content: []protocol.Content{&protocol.TextContent{Text: string(payload)}}}
	}
	server := protocol.NewServer(&protocol.Implementation{Name: "notes", Version: "1.0.0"}, nil)
	server.AddTool(&protocol.Tool{Name: "list_notes", Description: "List the user's notes.", Annotations: &protocol.ToolAnnotations{ReadOnlyHint: true},
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
		func(ctx context.Context, request *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			mu.Lock()
			defer mu.Unlock()
			return result(map[string]any{"notes": notes}), nil
		})
	server.AddTool(&protocol.Tool{Name: "add_note", Description: "Add a note to the user's notes.",
		InputSchema: map[string]any{"type": "object", "required": []string{"text"}, "properties": map[string]any{"text": map[string]any{"type": "string"}}}},
		func(ctx context.Context, request *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			var input struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(request.Params.Arguments, &input)
			mu.Lock()
			notes = append(notes, input.Text)
			mu.Unlock()
			log.Printf("NOTE ADDED: %s", input.Text)
			return result(map[string]any{"status": "added"}), nil
		})
	handler := protocol.NewStreamableHTTPHandler(func(*http.Request) *protocol.Server { return server }, &protocol.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	http.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if *token != "" && strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") != *token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	})
	http.HandleFunc("/notes", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewEncoder(w).Encode(notes)
	})
	log.Printf("mcp-notes on http://%s/mcp", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
