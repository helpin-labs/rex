package rex

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/helpin-ai/agent-runtime/memory"
)

// Memory extraction runs on the same model as the bot whose conversation is
// being remembered, so a local server never has to swap models between a
// chat and the memory work that follows it. Retrieval does not use a chat
// model at all: it only embeds the query, with the one embedding model that
// stored the facts.

type extractionModelKey struct{}

func withExtractionModel(ctx context.Context, model string) context.Context {
	if strings.TrimSpace(model) == "" {
		return ctx
	}
	return context.WithValue(ctx, extractionModelKey{}, model)
}

// extractorPool builds one FactExtractor per model on the memory endpoint.
type extractorPool struct {
	baseURL, apiKey, embeddingModel string
	local                           bool
	base                            *FactExtractor

	mu    sync.Mutex
	byKey map[string]*FactExtractor
}

func (p *extractorPool) forModel(model string) *FactExtractor {
	model = strings.TrimSpace(model)
	if model == "" || model == p.base.model {
		return p.base
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if extractor, ok := p.byKey[model]; ok {
		return extractor
	}
	provider, err := memory.NewProvider(memory.ProviderConfig{
		BaseURL: p.baseURL, APIKey: p.apiKey, ExtractionModel: model, EmbeddingModel: p.embeddingModel,
		ExtractionOptions: extractionOptions(map[bool]string{true: ProviderLocal, false: ProviderOpenAI}[p.local]),
		Client:            &http.Client{Timeout: 10 * time.Minute},
	})
	if err != nil {
		return p.base
	}
	extractor := NewFactExtractor(provider, p.baseURL, p.apiKey, model)
	extractor.local = p.local
	p.byKey[model] = extractor
	return extractor
}

func (p *extractorPool) forContext(ctx context.Context) *FactExtractor {
	model, _ := ctx.Value(extractionModelKey{}).(string)
	return p.forModel(model)
}

// routingExtractor is the single extractor the memory store sees. Extraction
// uses the model carried in the context; consolidation and reflection use
// the base model.
type routingExtractor struct{ pool *extractorPool }

func (r routingExtractor) Extract(ctx context.Context, request memory.RetainRequest) ([]memory.ExtractedFact, error) {
	return r.pool.forContext(ctx).Extract(ctx, request)
}

func (r routingExtractor) Consolidate(ctx context.Context, facts []memory.Fact, observations []memory.Observation) (memory.ConsolidationPlan, error) {
	return r.pool.base.Provider.Consolidate(ctx, facts, observations)
}

func (r routingExtractor) ReconcileObservations(ctx context.Context, newText, existing string) (memory.ObservationReconciliation, error) {
	return r.pool.base.Provider.ReconcileObservations(ctx, newText, existing)
}

func (r routingExtractor) ReflectionStep(ctx context.Context, messages []memory.ReflectionMessage, forcedTool string) (memory.ReflectionMessage, error) {
	return r.pool.base.Provider.ReflectionStep(ctx, messages, forcedTool)
}

// extractionModelForBank picks the chat model of the bot that owns a bank.
// Shared banks and bots on a different provider than memory use the base.
func (m *MemoryService) extractionModelForBot(ctx context.Context, botID string) string {
	settings, err := m.settings.Get(ctx)
	if err != nil || settings.Memory.ExtractionModel != "" {
		return "" // an explicit memory model always wins
	}
	bot, err := m.store.GetBot(ctx, botID)
	if err != nil || bot.Provider != settings.Memory.Provider || bot.Model == "" {
		return ""
	}
	return bot.Model
}

func (m *MemoryService) extractionModelForBank(ctx context.Context, bank string) string {
	if botID, ok := strings.CutPrefix(bank, "bot:"); ok {
		return m.extractionModelForBot(ctx, botID)
	}
	return ""
}

// baseExtractionModel is the configured memory model, or when memory follows
// the chat model, the default chat model or else the first bot's model on
// the memory provider.
func (m *MemoryService) baseExtractionModel(ctx context.Context, settings Settings) string {
	if model := strings.TrimSpace(settings.Memory.ExtractionModel); model != "" {
		return model
	}
	if settings.DefaultProvider == settings.Memory.Provider && settings.DefaultModel != "" {
		return settings.DefaultModel
	}
	if bots, err := m.store.ListBots(ctx); err == nil {
		for _, bot := range bots {
			if bot.Provider == settings.Memory.Provider && bot.Model != "" {
				return bot.Model
			}
		}
	}
	return ""
}
