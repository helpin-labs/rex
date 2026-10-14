package rex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/helpin-ai/agent-runtime-go/chatgptauth"
	"github.com/helpin-ai/agent-runtime/memory"
)

func TestLearningUsesOwningBotProviderForExtractionAndConsolidation(t *testing.T) {
	for _, provider := range []string{ProviderChatGPT, ProviderOpenRouter, ProviderAnthropic, ProviderOpenAI, ProviderLocal} {
		t.Run(provider, func(t *testing.T) {
			h := newSetupTestHost(t)
			bot := Bot{ID: "owner", Name: "Owner", Provider: provider, Model: "owner-model", MemoryMode: MemoryPrivate, ContextWindow: 32768}
			if err := h.Store.SaveBot(t.Context(), &bot); err != nil {
				t.Fatal(err)
			}
			if provider == ProviderChatGPT {
				if err := h.Settings.ChatGPT.saveToken(&chatgptauth.Token{AccessToken: "fake-oauth", AccountID: "fake-account", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
			} else if provider != ProviderLocal {
				if err := h.Settings.secrets.Set(secretKey(provider), "fake-key"); err != nil {
					t.Fatal(err)
				}
			}
			var agent Agent
			var starts atomic.Int32
			var system string
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/agents/"):
					_ = json.NewDecoder(r.Body).Decode(&agent)
					system = agent.SystemPrompt
					if agent.Provider != bot.RuntimeProvider() || len(agent.AllowedTools) != 0 || len(agent.Skills) != 0 {
						t.Error("learning changed provider or gained tools")
					}
					_ = json.NewEncoder(w).Encode(agent)
				case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
					var body struct {
						Model           RunModel         `json:"model"`
						ModelCredential *ModelCredential `json:"model_credential"`
						MCPServers      []RunMCPServer   `json:"mcp_servers"`
					}
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body.Model.Provider != bot.RuntimeProvider() || body.Model.Model != bot.Model || len(body.MCPServers) != 0 {
						t.Error("learning did not use the owning bot model")
					}
					if provider == ProviderChatGPT && (body.ModelCredential == nil || body.ModelCredential.Type != "oauth" || body.ModelCredential.AccessToken != "fake-oauth") {
						t.Error("OAuth credential not forwarded")
					}
					starts.Add(1)
					fmt.Fprint(w, `{"id":"helper","status":"running"}`)
				case strings.HasSuffix(r.URL.Path, "/messages"):
					content := `{"facts":[{"text":"A recorded task remains unfinished.","kind":"unfinished","sources":["request/proof"],"entities":[]}]}`
					if strings.Contains(system, "Consolidate this bot") {
						content = `{"creates":[],"updates":[],"deletes":[]}`
					}
					_ = json.NewEncoder(w).Encode([]Message{{Role: "assistant", Content: content}})
				default:
					fmt.Fprint(w, `{"id":"helper","status":"completed"}`)
				}
			}))
			defer runtime.Close()
			h.Runtime = newTestRuntimeClient(t, runtime.URL, "")
			h.SetRuntimeReady(true)
			ctx := context.WithValue(t.Context(), learningBankKey{}, bot.MemoryBank())
			router := routingExtractor{service: h.Memory}
			evidence, _ := json.Marshal(operationalEvidence{Entries: []HistoryEntry{{ID: "request/proof", TurnID: "proof", Kind: "user_request", Text: "Continue recorded work"}}})
			facts, err := router.Extract(ctx, memory.RetainRequest{Content: string(evidence), Metadata: map[string]string{"source": "episode", "bot_id": bot.ID, "turn_id": "proof"}})
			if err != nil || len(facts) != 1 {
				t.Fatalf("facts=%+v err=%v", facts, err)
			}
			if _, err := router.Consolidate(ctx, []memory.Fact{{ID: "fact", Type: "experience", Text: facts[0].Text}}, nil); err != nil {
				t.Fatal(err)
			}
			if starts.Load() != 2 {
				t.Fatalf("helper runs=%d", starts.Load())
			}
		})
	}
}

func TestSharedLearningKeepsSourceBotAndUsesRexForConsolidation(t *testing.T) {
	h := newSetupTestHost(t)
	main := Bot{ID: "rex", Name: "Rex", Role: RoleMain, Provider: ProviderOpenRouter, Model: "rex-model", MemoryMode: MemoryPrivate}
	worker := Bot{ID: "worker", Name: "Worker", Provider: ProviderChatGPT, Model: "worker-model", MemoryMode: MemoryShared}
	for _, bot := range []Bot{main, worker} {
		if err := h.Store.SaveBot(t.Context(), &bot); err != nil {
			t.Fatal(err)
		}
	}
	bot, err := h.Memory.learningBot(t.Context(), sharedBank, worker.ID)
	if err != nil || bot.ID != worker.ID {
		t.Fatalf("source owner=%+v %v", bot, err)
	}
	bot, err = h.Memory.learningBot(t.Context(), sharedBank, "")
	if err != nil || bot.ID != main.ID {
		t.Fatalf("shared consolidation owner=%+v %v", bot, err)
	}
	if _, err := h.Memory.learningBot(t.Context(), main.MemoryBank(), worker.ID); err == nil {
		t.Fatal("cross-bank learning accepted")
	}
	settings, _ := h.Settings.Get(t.Context())
	settings.DefaultProvider, settings.DefaultModel = ProviderChatGPT, "default-chat-model"
	if _, _, err := h.Settings.Save(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	worker.Model = ""
	if got := h.Memory.learningModelForBot(t.Context(), worker); got != "default-chat-model" {
		t.Fatalf("default model=%s", got)
	}
}

func TestCancelledLearningCancelsRuntimeHelper(t *testing.T) {
	h := newSetupTestHost(t)
	ctx, cancel := context.WithCancel(t.Context())
	var cancelled atomic.Int32
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/cancel") {
			cancelled.Add(1)
			fmt.Fprint(w, `{"id":"helper","status":"cancelled"}`)
			return
		}
		if r.Method == http.MethodGet {
			cancel()
		}
		fmt.Fprint(w, `{"id":"helper","status":"running"}`)
	}))
	defer runtime.Close()
	h.Runtime = newTestRuntimeClient(t, runtime.URL, "")
	_, err := h.Memory.generateLearning(ctx, Bot{ID: "owner", Name: "Owner", Provider: ProviderChatGPT}, &RunModel{Provider: "openai_chatgpt", Model: "owner-model"}, nil, ResolvedContext{}, "Return JSON", "Evidence")
	if err == nil || cancelled.Load() != 1 {
		t.Fatalf("error=%v cancellation=%d", err, cancelled.Load())
	}
}
