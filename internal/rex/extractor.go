package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/helpin-ai/agent-runtime/memory"
)

// FactExtractor wraps agent-runtime's memory provider. Extraction uses the
// provider's structured schema, with a simple-prompt fallback for models
// that cannot follow it. Embedding, consolidation and reflection are served
// by the embedded memory.Provider.
type FactExtractor struct {
	*memory.Provider
	baseURL string
	apiKey  string
	model   string
	local   bool
	client  *http.Client
	// structuredFailed remembers that this model could not follow the
	// runtime's structured schema, so later saves skip straight to the
	// simple prompt instead of waiting for the same timeout again.
	structuredFailed atomic.Bool
}

func NewFactExtractor(provider *memory.Provider, baseURL, apiKey, model string) *FactExtractor {
	return &FactExtractor{Provider: provider, baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, model: model, client: &http.Client{Timeout: 10 * time.Minute}}
}

const extractorPrompt = `You maintain the long-term memory of a personal assistant. From the conversation excerpt, extract durable facts about the user that will still be useful in future conversations: preferences, personal details, people they know, projects, plans, commitments and decisions.

Rules:
- Each fact is one short, self-contained sentence in the third person, starting with "The user" where natural (for example "The user prefers answers as bullet points.").
- When the user changes or corrects an earlier preference, write the new state explicitly, for example "The user now prefers metric units instead of imperial."
- Include the user's name if they state it.
- Skip greetings, small talk, questions without personal information, anything the assistant said on its own, and anything temporary (today's weather, a one-off calculation).
- Never record passwords, API keys or other secrets.
- entities: up to 5 short names of people, places, products or topics mentioned in the fact.
- If there is nothing durable, return an empty list.

Answer only with JSON: {"facts":[{"text":"...","entities":["..."]}]}`

// Extract uses agent-runtime's memory extraction first: its schema captures
// entities, dates and causal links. Models that cannot follow that strict
// schema in time fall back to a small prompt so memory still works.
func (e *FactExtractor) Extract(ctx context.Context, request memory.RetainRequest) ([]memory.ExtractedFact, error) {
	if e.structuredFailed.Load() {
		return e.extractSimple(ctx, request)
	}
	upstreamCtx, cancel := context.WithTimeout(ctx, upstreamExtractionTimeout)
	facts, err := e.Provider.Extract(upstreamCtx, request)
	cancel()
	if err == nil {
		return facts, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	e.structuredFailed.Store(true)
	slog.Warn("memory extraction fell back to the simple prompt; using it for this model from now on", "model", e.model, "error", err)
	return e.extractSimple(ctx, request)
}

// upstreamExtractionTimeout bounds the structured extraction. A capable
// model answers in seconds; a model stuck in constrained decoding never does.
var upstreamExtractionTimeout = 90 * time.Second

func (e *FactExtractor) extractSimple(ctx context.Context, request memory.RetainRequest) ([]memory.ExtractedFact, error) {
	date := request.Timestamp
	if date.IsZero() {
		date = time.Now()
	}
	user := fmt.Sprintf("Date: %s\nContext: %s\n\nExcerpt:\n%s", date.Format("Monday, 2 January 2006"), firstNonEmpty(request.Context, "none"), request.Content)
	// Plain JSON prompting, not response_format: grammar-constrained decoding
	// makes some local models (gpt-oss in LM Studio) generate without end.
	var lastErr error
	for _, temperature := range []float64{0.1, 0.4, 0.7} {
		body := map[string]any{
			"model": e.model, "temperature": temperature,
			"messages": []map[string]string{{"role": "system", "content": extractorPrompt}, {"role": "user", "content": user}},
		}
		callCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		facts, err := e.call(callCtx, body, date)
		cancel()
		if err == nil {
			return facts, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		lastErr = err
	}
	return nil, fmt.Errorf("fact extraction failed: %w", lastErr)
}

var jsonObject = regexp.MustCompile(`(?s)\{.*\}`)

func (e *FactExtractor) call(ctx context.Context, body map[string]any, date time.Time) ([]memory.ExtractedFact, error) {
	headers := map[string]string{}
	if e.apiKey != "" {
		headers["Authorization"] = "Bearer " + e.apiKey
	}
	var response struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := httpJSON(ctx, http.MethodPost, e.baseURL+"/chat/completions", headers, body, &response); err != nil {
		return nil, err
	}
	if len(response.Choices) == 0 {
		return nil, errors.New("empty extraction response")
	}
	content := strings.TrimSpace(response.Choices[0].Message.Content)
	// Some models wrap JSON in prose or code fences; take the object.
	if match := jsonObject.FindString(content); match != "" {
		content = match
	}
	var parsed struct {
		Facts []struct {
			Text     string   `json:"text"`
			Entities []string `json:"entities"`
		} `json:"facts"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("invalid extraction JSON: %w", err)
	}
	facts := []memory.ExtractedFact{}
	for _, item := range parsed.Facts {
		text := strings.TrimSpace(item.Text)
		if !plausibleText(text) {
			continue
		}
		var entities []string
		for _, entity := range item.Entities {
			entity = strings.TrimSpace(entity)
			if plausibleText(entity) && len(entity) <= 60 {
				entities = append(entities, entity)
			}
		}
		if entities == nil {
			entities = []string{}
		}
		facts = append(facts, memory.ExtractedFact{Text: redactSecrets(text), Type: "world", Entities: entities})
	}
	if len(parsed.Facts) > 0 && len(facts) == 0 {
		return nil, errors.New("extraction produced only malformed facts")
	}
	return facts, nil
}

// plausibleText rejects the degenerate output constrained decoding can
// produce: runs of ellipses, question marks or replacement characters.
func plausibleText(text string) bool {
	if text == "" || !utf8.ValidString(text) || len(text) > 600 {
		return false
	}
	letters, junk := 0, 0
	for _, r := range text {
		switch {
		case r == '…' || r == '?' || r == '�':
			junk++
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r > 127 && r != '…':
			letters++
		}
	}
	return letters >= 2 && junk*4 <= letters
}
