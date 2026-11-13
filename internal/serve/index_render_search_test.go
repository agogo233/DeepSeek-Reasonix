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

// The web client presents a chat-style layout: user messages render as a
// right-aligned tinted bubble, assistant messages as a labelled card, and the
// tool/error surfaces share the same reading column. These content assertions
// lock the layout markers so a future edit cannot silently drop the role
// distinction or the column alignment.
func TestServeIndexChatLayout(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		// User bubble: right-aligned, tinted, caret hidden.
		".msg--user{display:flex;justify-content:flex-end}",
		".msg__caret{display:none}",
		".msg--user .msg__text{max-width:85%;background:var(--bubble-user)",
		"--bubble-user:oklch(68% .16 38/.13)",
		// Assistant card + role label.
		".msg--assistant{color:var(--fg);line-height:1.7;background:var(--panel)",
		".msg__head{display:flex",
		".msg__dot{width:7px;height:7px;border-radius:50%",
		"h.appendChild(document.createTextNode('Reasonix'))",
		// Surfaces that must share the 760px reading column.
		".card{max-width:760px;",
		".msg--error{max-width:760px;",
		// Code blocks use the dedicated deeper background.
		".msg__md pre{background:var(--code-bg)",
		"--code-bg:oklch(13.5% .006 275)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("serve index missing chat layout marker %q", want)
		}
	}
}

// The "back to latest" control lets a reader who scrolled up return to the
// newest message. It must stay anchored to the footer (the transcript is wiped
// on session switch), stay hidden until the reader is genuinely far from the
// bottom, and carry a localized accessible name.
func TestServeIndexJumpLatest(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		`id="jump-latest"`,
		`title="__('jump_latest')"`,
		".jump-latest{position:absolute;bottom:calc(100% + 14px);right:22px;width:34px;height:34px;border-radius:50%",
		".jump-latest--show{opacity:1;visibility:visible",
		"'jump_latest': 'Back to latest'",
		"'jump_latest': '回到最新'",
		"const jumpLatest = $('#jump-latest');",
		"function updateJumpLatest()",
		"function scrollDown(force) { updateJumpLatest();",
		"log.scrollTo({top:log.scrollHeight,behavior:'instant'});updateJumpLatest();",
		"showWelcome() { if(welcome) welcome.style.display='';updateJumpLatest();}",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("serve index missing jump-latest marker %q", want)
		}
	}
}
