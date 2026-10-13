package rex

import (
	"strings"
	"testing"
)

func TestBoundCarryKeepsCurrentRequestAndArchiveReference(t *testing.T) {
	current := "New requirements: use official sources; do not publish."
	input := "<previous_conversation>\n" + strings.Repeat("Earlier work 界", 500) + "\n</previous_conversation>\n\nNew user message:\n" + current
	bounded := boundCarriedContext(input, 300)
	if !strings.HasSuffix(bounded, current) || !strings.Contains(bounded, "history_search/history_read") || len([]rune(bounded)) > 600 {
		t.Fatal("bounded carry lost protected request or overflowed")
	}
	if boundCarriedContext(current, 10) != current {
		t.Fatal("current request silently truncated")
	}
}
