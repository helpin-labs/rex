package rex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type ContextPolicy struct {
	MaxTotalTokens   int64 `json:"max_total_tokens,omitempty"`
	Enabled          bool  `json:"enabled"`
	ContextWindow    int   `json:"context_window,omitempty"`
	InputLimit       int   `json:"input_limit,omitempty"`
	MaxOutputTokens  int   `json:"max_output_tokens,omitempty"`
	TriggerTokens    int   `json:"trigger_tokens,omitempty"`
	KeepRecentTokens int   `json:"keep_recent_tokens,omitempty"`
	SummaryTokens    int   `json:"summary_tokens,omitempty"`
	UserAnchorTokens int   `json:"user_anchor_tokens,omitempty"`
	SafetyTokens     int   `json:"safety_tokens,omitempty"`
}

func contextPolicy(window, maxOutput int) ContextPolicy {
	if window < 4096 || window > 4000000 {
		return ContextPolicy{}
	}
	output := min(8192, window/4)
	if maxOutput > 0 {
		output = min(output, maxOutput)
	}
	safety := min(4096, window/16)
	input := window - output - safety
	trigger := min(64000, input)
	return ContextPolicy{Enabled: true, ContextWindow: window, InputLimit: input, MaxOutputTokens: output, SafetyTokens: safety,
		TriggerTokens: trigger, KeepRecentTokens: min(16000, trigger/4), SummaryTokens: min(4000, trigger/8), UserAnchorTokens: min(4000, trigger/8)}
}

type ResolvedContext struct {
	ContextPolicy
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Source   string `json:"source"`
	Warning  string `json:"warning,omitempty"`
}

func (h *Host) resolveContext(ctx context.Context, bot Bot, model *RunModel) ResolvedContext {
	id := bot.Model
	if model != nil {
		id = model.Model
	}
	result := ResolvedContext{Provider: bot.Provider, Model: id, Source: "unknown"}
	if bot.ContextWindow > 0 {
		result.ContextPolicy = contextPolicy(bot.ContextWindow, 0)
		result.Source = "configured"
		return result
	}

	endpoint, key, _ := h.providerEndpoint(ctx, bot.Provider)
	identity := endpoint + "|" + key
	if bot.Provider == ProviderChatGPT {
		credential, _ := h.Settings.ChatGPT.Credential(ctx, false, "")
		if credential != nil {
			identity = credential.AccountID
		}
	}
	hash := sha256.Sum256([]byte(identity))
	cacheKey := fmt.Sprintf("context_model_catalog.%s.%x", bot.Provider, hash[:8])
	var cached struct {
		Fetched time.Time
		Models  []ModelInfo
	}
	raw, _ := h.Store.GetSetting(ctx, cacheKey)
	_ = json.Unmarshal([]byte(raw), &cached)
	ttl := time.Hour
	if bot.Provider == ProviderLocal {
		ttl = time.Minute
	}
	if time.Since(cached.Fetched) > ttl || len(cached.Models) == 0 {
		probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		models, err := h.ListModels(probeCtx, bot.Provider)
		cancel()
		if err != nil {
			cached.Models = nil
		}
		if err == nil {
			cached.Fetched = time.Now()
			cached.Models = models
			body, _ := json.Marshal(cached)
			_ = h.Store.SetSetting(ctx, cacheKey, string(body))
		}
	}
	for _, info := range cached.Models {
		if info.ID == id && info.Context >= 4096 {
			result.ContextPolicy = contextPolicy(info.Context, info.MaxOutput)
			result.Source = "provider"
			return result
		}
	}
	result.Warning = "Automatic context compaction needs the model's verified context window. Set it in this bot's model settings if the provider does not report it."
	return result
}

func (h *Host) runAgent(ctx context.Context, bot Bot, model *RunModel) (Agent, ResolvedContext) {
	settings, _ := h.Settings.Get(ctx)

	bot.BrowserBackend = bot.browserMode(settings)
	agent := bot.RuntimeAgent(bot.SystemPrompt(h.Skills.Resolve(bot.Skills)))
	resolved := h.resolveContext(ctx, bot, model)
	var config map[string]any
	_ = json.Unmarshal(agent.ExecutionConfig, &config)
	if config == nil {
		config = map[string]any{}
	}
	config["native_context"] = resolved.ContextPolicy
	agent.ExecutionConfig, _ = json.Marshal(config)
	body, _ := json.Marshal(resolved)
	_ = h.Store.SetSetting(ctx, "context_policy."+bot.ID, string(body))
	return agent, resolved
}

func (h *Host) chatGPTModelMetadata(ctx context.Context) ([]ModelInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	credential, err := h.Settings.ChatGPT.Credential(ctx, false, "")
	if err != nil {
		return nil, err
	}
	headers := map[string]string{"Authorization": "Bearer " + credential.AccessToken}
	if credential.AccountID != "" {
		headers["ChatGPT-Account-Id"] = credential.AccountID
	}
	var catalog struct {
		Models []struct {
			Slug             string `json:"slug"`
			ContextWindow    int    `json:"context_window"`
			EffectivePercent int    `json:"effective_context_window_percent"`
		} `json:"models"`
	}
	if err := httpJSON(ctx, "GET", "https://chatgpt.com/backend-api/codex/models?client_version=0.162.0", headers, nil, &catalog); err != nil {
		return nil, err
	}
	var models []ModelInfo
	for _, item := range catalog.Models {
		window := item.ContextWindow
		if item.EffectivePercent > 0 && item.EffectivePercent <= 100 {
			window = window * item.EffectivePercent / 100
		}
		models = append(models, ModelInfo{ID: item.Slug, Context: window})
	}
	return models, nil
}

func (h *Host) localModelLimits(ctx context.Context, base string, models []ModelInfo) {
	// LM Studio's loaded context can be smaller than the advertised model maximum.
	var response struct {
		Data []struct {
			ID     string `json:"id"`
			Loaded int    `json:"loaded_context_length"`
		} `json:"data"`
	}
	probe, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if httpJSON(probe, "GET", strings.TrimSuffix(base, "/v1")+"/api/v0/models", nil, nil, &response) != nil {
		return
	}
	for _, item := range response.Data {
		for i := range models {
			if models[i].ID == item.ID && item.Loaded >= 4096 {
				models[i].Context = item.Loaded
			}
		}
	}
}

func contextEvidence(policy ResolvedContext) string {
	if !policy.Enabled {
		return policy.Warning
	}
	return fmt.Sprintf("Compaction enabled for %s: %d token context, %d token input trigger", policy.Model, policy.ContextWindow, policy.TriggerTokens)
}

func minimumReported(values ...int) int {
	result := 0
	for _, value := range values {
		if value > 0 && (result == 0 || value < result) {
			result = value
		}
	}
	return result
}

func boundCarriedContext(instructions string, budget int) string {
	const endMarker = "\n</previous_conversation>\n\nNew user message:\n"
	if !strings.HasPrefix(instructions, "<previous_conversation>\n") {
		return instructions
	}
	end := strings.LastIndex(instructions, endMarker)
	if end < 0 {
		return instructions
	}
	carry := instructions[len("<previous_conversation>\n"):end]
	if len([]rune(carry)) <= budget {
		return instructions
	}
	runes := []rune(carry)
	keep := min(len(runes), max(0, budget))
	return "<previous_conversation>\nEarlier context is bounded; use history_search/history_read for full recorded evidence.\n" + string(runes[len(runes)-keep:]) + instructions[end:]
}
