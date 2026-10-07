package rex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type ModelInfo struct {
	ID        string `json:"id"`
	Embedding bool   `json:"embedding"`
}

var providerDefaults = map[string]string{
	ProviderOpenAI:     "https://api.openai.com/v1",
	ProviderOpenRouter: "https://openrouter.ai/api/v1",
	ProviderAnthropic:  "https://api.anthropic.com",
}

func (h *Host) providerEndpoint(ctx context.Context, provider string) (string, string, error) {
	settings, err := h.Settings.Get(ctx)
	if err != nil {
		return "", "", err
	}
	config, ok := settings.providers()[provider]
	if !ok {
		return "", "", fmt.Errorf("unknown provider %q", provider)
	}
	base := firstNonEmpty(config.BaseURL, providerDefaults[provider])
	key, err := h.Settings.APIKey(provider)
	return strings.TrimRight(base, "/"), key, err
}

// ListModels asks a provider which models it offers.
func (h *Host) ListModels(ctx context.Context, provider string) ([]ModelInfo, error) {
	base, key, err := h.providerEndpoint(ctx, provider)
	if err != nil {
		return nil, err
	}
	if provider != ProviderLocal && key == "" {
		return nil, fmt.Errorf("add a %s API key first", providerLabel(provider))
	}
	endpoint := base + "/models"
	headers := map[string]string{}
	switch provider {
	case ProviderAnthropic:
		endpoint = base + "/v1/models?limit=100"
		headers["x-api-key"] = key
		headers["anthropic-version"] = "2023-06-01"
	default:
		if key != "" {
			headers["Authorization"] = "Bearer " + key
		}
	}
	var response struct {
		Data []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"data"`
	}
	if err := httpJSON(ctx, http.MethodGet, endpoint, headers, nil, &response); err != nil {
		return nil, err
	}
	models := make([]ModelInfo, 0, len(response.Data))
	for _, item := range response.Data {
		lower := strings.ToLower(item.ID)
		models = append(models, ModelInfo{ID: item.ID, Embedding: strings.Contains(lower, "embed") || item.Type == "embeddings"})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

type ProviderTestResult struct {
	OK        bool   `json:"ok"`
	Reachable bool   `json:"reachable"`
	ToolCalls bool   `json:"tool_calls"`
	Models    int    `json:"models"`
	Message   string `json:"message"`
	LatencyMS int64  `json:"latency_ms"`
}

// TestProvider checks reachability and, for OpenAI-compatible endpoints,
// that the chosen model can call a tool, which every bot depends on.
func (h *Host) TestProvider(ctx context.Context, provider, model string) ProviderTestResult {
	started := time.Now()
	models, err := h.ListModels(ctx, provider)
	if err != nil {
		return ProviderTestResult{Message: "Could not reach " + providerLabel(provider) + ": " + err.Error()}
	}
	result := ProviderTestResult{Reachable: true, Models: len(models)}
	if model == "" || provider == ProviderAnthropic {
		result.OK = true
		result.Message = fmt.Sprintf("Connected. %d models available.", len(models))
		result.LatencyMS = time.Since(started).Milliseconds()
		return result
	}
	base, key, _ := h.providerEndpoint(ctx, provider)
	headers := map[string]string{}
	if key != "" {
		headers["Authorization"] = "Bearer " + key
	}
	request := map[string]any{
		"model":       model,
		"temperature": 0,
		"messages":    []map[string]string{{"role": "user", "content": "What is the weather in Paris? Use the tool."}},
		"tools": []map[string]any{{"type": "function", "function": map[string]any{
			"name": "get_weather", "description": "Get the current weather for a city.",
			"parameters": map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}, "required": []string{"city"}},
		}}},
		"tool_choice": "auto",
	}
	var response struct {
		Choices []struct {
			Message struct {
				ToolCalls []struct {
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	testCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := httpJSON(testCtx, http.MethodPost, base+"/chat/completions", headers, request, &response); err != nil {
		result.Message = "The server is reachable, but the model failed: " + err.Error()
		return result
	}
	result.LatencyMS = time.Since(started).Milliseconds()
	for _, choice := range response.Choices {
		for _, call := range choice.Message.ToolCalls {
			if call.Function.Name == "get_weather" {
				result.ToolCalls = true
			}
		}
	}
	if !result.ToolCalls {
		result.Message = "Connected, but " + model + " did not call a tool. Bots need a tool-capable model (for example Qwen 3, gpt-oss or Llama 3.1+)."
		return result
	}
	result.OK = true
	result.Message = fmt.Sprintf("Connected. %s can call tools (%.1fs).", model, float64(result.LatencyMS)/1000)
	return result
}

func httpJSON(ctx context.Context, method, endpoint string, headers map[string]string, input, output any) error {
	var body io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(payload))
		if len(message) > 300 {
			message = message[:300]
		}
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, message)
	}
	if output != nil {
		if err := json.Unmarshal(payload, output); err != nil {
			return errors.New("unexpected response from the server")
		}
	}
	return nil
}
