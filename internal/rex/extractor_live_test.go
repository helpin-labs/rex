package rex

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/helpin-ai/agent-runtime/memory"
)

// Run with REX_LIVE_MODEL_URL=http://127.0.0.1:1234/v1 REX_LIVE_MODEL=<id>.
func TestFactExtractorLive(t *testing.T) {
	base, model := os.Getenv("REX_LIVE_MODEL_URL"), os.Getenv("REX_LIVE_MODEL")
	if base == "" || model == "" {
		t.Skip("live model not configured")
	}
	provider, err := memory.NewProvider(memory.ProviderConfig{BaseURL: base, ExtractionModel: model, EmbeddingModel: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	extractor := NewFactExtractor(provider, base, "", model)
	cases := map[string]string{
		"preference": "The user said to their assistant Rex: Hi! My name is Amad and I always prefer answers in bullet points. What is 2+2?",
		"correction": "The user said to their assistant Rex: Actually, forget bullet points, I now prefer short plain paragraphs.",
	}
	for name, content := range cases {
		started := time.Now()
		facts, err := extractor.Extract(context.Background(), memory.RetainRequest{DocumentID: name, Content: content, Context: "Personal assistant chat.", Timestamp: time.Now()})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Logf("%s (%s):", name, time.Since(started).Round(time.Millisecond))
		joined := ""
		for _, fact := range facts {
			t.Logf("  %q %v", fact.Text, fact.Entities)
			joined += strings.ToLower(fact.Text) + " "
		}
		if len(facts) == 0 {
			t.Fatalf("%s: no facts extracted", name)
		}
	}
}
