package serve

import (
	"strings"
	"testing"
)

// A page reload replays pending prompts over SSE before /history renders, so
// the history renderer must move prompt cards back below the transcript
// instead of leaving them stranded above the conversation and out of view.
func TestServeIndexRendersHistoryBelowPendingPromptCards(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		"log.querySelectorAll('.ask,.approval').forEach(c=>log.appendChild(c));",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("serve index missing pending prompt re-mount %q", want)
		}
	}
}

// Same-page SSE reconnects re-emit every pending prompt; a replayed prompt
// with the same id must replace its old card instead of stacking duplicates.
func TestServeIndexReplayedPromptReplacesCard(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		"replaceStalePromptCard(a.id);",
		"replaceStalePromptCard(ask.id);",
		"if(a.id)promptCardCleanups[a.id]=cleanup;",
		"if(ask.id)promptCardCleanups[ask.id]=cleanup;",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("serve index missing prompt card dedup %q", want)
		}
	}
}
