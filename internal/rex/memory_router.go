package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/helpin-ai/agent-runtime/memory"
)

type learningBankKey struct{}

type routingExtractor struct{ service *MemoryService }

func (r routingExtractor) Extract(ctx context.Context, request memory.RetainRequest) ([]memory.ExtractedFact, error) {
	bank, _ := ctx.Value(learningBankKey{}).(string)
	extractor, err := r.service.learningExtractor(ctx, bank, request.Metadata["bot_id"])
	if err != nil {
		return nil, err
	}
	return extractor.Extract(ctx, request)
}

func (r routingExtractor) Consolidate(ctx context.Context, facts []memory.Fact, observations []memory.Observation) (memory.ConsolidationPlan, error) {
	bank, _ := ctx.Value(learningBankKey{}).(string)
	extractor, err := r.service.learningExtractor(ctx, bank, "")
	if err != nil {
		return memory.ConsolidationPlan{}, err
	}
	return extractor.consolidateOperational(ctx, facts, observations)
}

func (m *MemoryService) learningBot(ctx context.Context, bank, botID string) (*Bot, error) {
	if botID != "" {
		bot, err := m.store.GetBot(ctx, botID)
		if err != nil || bot.MemoryBank() != bank {
			return nil, errors.New("learning source does not belong to this bot's bank")
		}
		return bot, nil
	}
	if id, ok := strings.CutPrefix(bank, "bot:"); ok {
		return m.store.GetBot(ctx, id)
	}
	if bank == sharedBank && m.host != nil {
		if bot := m.host.MainBot(ctx); bot != nil {
			return bot, nil
		}
	}
	return nil, errors.New("memory bank has no owning bot")
}

func (m *MemoryService) learningModelForBot(ctx context.Context, bot Bot) string {
	if bot.Model != "" {
		return bot.Model
	}
	settings, err := m.settings.Get(ctx)
	if err != nil {
		return ""
	}
	if bot.Provider == settings.DefaultProvider && settings.DefaultModel != "" {
		return settings.DefaultModel
	}
	if bot.Provider == ProviderChatGPT {
		return ChatGPTModels[0]
	}
	return "Provider default"
}

func (m *MemoryService) learningExtractor(ctx context.Context, bank, botID string) (*FactExtractor, error) {
	if m.host == nil || !m.host.RuntimeReady() {
		return nil, errors.New("bot engine is not ready for learning")
	}
	bot, err := m.learningBot(ctx, bank, botID)
	if err != nil {
		return nil, err
	}
	model, credential, err := m.settings.RunModelFor(ctx, *bot)
	if err != nil {
		return nil, err
	}
	resolved := m.host.resolveContext(ctx, *bot, model)
	extractor := &FactExtractor{model: m.learningModelForBot(ctx, *bot), inputLimit: resolved.InputLimit}
	extractor.generate = func(ctx context.Context, system, evidence string) (string, error) {
		return m.generateLearning(ctx, *bot, model, credential, resolved, system, evidence)
	}
	return extractor, nil
}

// Learning uses the same run credential path as chat, with no tools or MCPs.
func (m *MemoryService) generateLearning(ctx context.Context, bot Bot, model *RunModel, credential *ModelCredential, resolved ResolvedContext, system, evidence string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	resolved.MaxTotalTokens = 100000
	if resolved.Enabled {
		resolved.MaxOutputTokens = min(resolved.MaxOutputTokens, 4096)
	}
	config, _ := json.Marshal(map[string]any{"native_context": resolved.ContextPolicy, "max_tool_steps": 1})
	id := bot.ID + "--learning"
	_, err := m.host.Runtime.UpsertAgent(ctx, Agent{ID: id, AppID: AppID, Name: bot.Name + " · memory", RuntimeKind: "native_sdk", Provider: bot.RuntimeProvider(), Model: bot.Model,
		SystemPrompt: system, AllowedTools: []string{}, AllowedTargets: []string{"routine"}, ApprovalMode: "never", ExecutionConfig: config, Skills: []SkillRef{}})
	if err != nil {
		return "", err
	}
	run, err := m.host.Runtime.StartRun(ctx, StartRunSpec{AgentID: id, HostRunID: id + "-" + randomHex(8), TargetType: "routine", TargetID: id, Instructions: evidence, Model: model, ModelCredential: credential,
		Metadata: map[string]any{"bot_id": bot.ID, "memory_learning": true}})
	if err != nil {
		return "", err
	}
	terminal := false
	defer func() {
		if !terminal {
			stop, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_, _ = m.host.Runtime.CancelRun(stop, run.ID)
		}
	}()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, err := m.host.Runtime.GetRun(ctx, run.ID)
		if err != nil {
			return "", err
		}
		if current.Terminal() {
			terminal = true
			if current.Status != "completed" {
				return "", fmt.Errorf("bot learning failed: %s", firstNonEmpty(current.ErrorMessage, current.Status))
			}
			messages, err := m.host.Runtime.ListMessages(ctx, run.ID)
			if err != nil {
				return "", err
			}
			for i := len(messages) - 1; i >= 0; i-- {
				if messages[i].Role == "assistant" && strings.TrimSpace(messages[i].Content) != "" {
					return messages[i].Content, nil
				}
			}
			return "", errors.New("bot learning returned no text")
		}
		if current.Status == "paused" {
			return "", fmt.Errorf("bot learning paused: %s", current.PauseReason)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}
