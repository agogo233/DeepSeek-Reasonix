package serve

import (
	"strings"
	"testing"
)

// The embedded web client renders assistant markdown through a safe,
// escape-first renderer. These content assertions lock the security and
// resilience markers so a future edit cannot silently weaken them.
func TestServeIndexRendersMarkdownSafely(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		"function mdRender(src)",
		"function mdInline(raw)",
		"function mdSafeUrl(raw, isImg)",
		"function mdHighlight(code, lang)",
		// Scheme allowlist: only http/https (link+image) and mailto (link).
		"if(s!=='http'&&s!=='https'&&s!=='mailto') return '';",
		// External links must open in a new, referrer-safe context.
		`target="_blank" rel="noopener noreferrer"`,
		// Images must never carry a referrer to the serve origin.
		`referrerpolicy="no-referrer"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("serve index missing markdown safety marker %q", want)
		}
	}
}

// Code fences render a copy button wired to the existing clipboard helper; the
// copy action reads the raw code text, never the escaped HTML.
func TestServeIndexCodeBlockCopy(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		`class="md-copy"`,
		"function bindCodeCopy(root)",
		"code.textContent",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("serve index missing code-copy marker %q", want)
		}
	}
}

// Streaming re-renders are throttled and degrade to plain text when the buffer
// grows past the guard, so a long turn never janks the client or loses content.
func TestServeIndexMarkdownStreamGuards(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		"const MD_RENDER_MS = 100, MD_PLAIN_LIMIT = 150000;",
		"function mdRenderNow()",
		"function mdSchedule()",
		"currentPlain = true",
		"currentRaw += (t||'');",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("serve index missing stream guard marker %q", want)
		}
	}
}

// Reasoning blocks render markdown too, but must never rebuild the collapse
// toggle; only the body element's innerHTML is replaced.
func TestServeIndexReasoningRendersMarkdown(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		"currentReasoning.innerHTML = mdRender(currentReasoningRaw);",
		`el('div','reasoning__body msg__md')`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("serve index missing reasoning markdown marker %q", want)
		}
	}
}

// In-session search wraps matches with DOM-level <mark> splitting (no
// innerHTML), so it stays safe and spans inline markdown boundaries.
func TestServeIndexSearchIsSafeAndBoundaryAware(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		"function searchApply(query)",
		"function searchWrapOne(node, ls, le)",
		"node.splitText(le)",
		"function searchBuildIndex(container)",
		"function clearSearchMarks()",
		"document.createTextNode(m.textContent)",
		"mark.className = 'srch'",
		"searchMarks.push(mark);\n  searchMatches.push(mark);",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("serve index missing search safety marker %q", want)
		}
	}
}

// Ctrl/Cmd+F opens the search bar and blocks the native browser find.
func TestServeIndexSearchShortcuts(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		"(e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'f'",
		"e.preventDefault(); openSearch();",
		"$('#btn-search').onclick = openSearch;",
		"function searchRefresh()",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("serve index missing search shortcut marker %q", want)
		}
	}
}
