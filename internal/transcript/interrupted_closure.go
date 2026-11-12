package transcript

import "time"

// interruptedResultText stands in for a tool result that never landed because
// the service restarted before the interrupted turn could close its call. It
// is display-only: the transcript package's records are never used to build
// provider requests, and the canonical provider stream already repairs its own
// dangling calls on load and send.
const interruptedResultText = "[no result: the previous turn was interrupted before this tool call completed]"

// RecoverUnresolvedToolCalls closes tool calls left without a result by a turn
// the service restarted mid-flight. A restarted runtime rebuilds its display
// projection from persisted messages, so a call whose result never committed is
// a dangling record that would otherwise render as a perpetual unknown card.
//
// Detection is by pairing, never by a Pending flag: history conversion does not
// propagate Pending, and the live pending state does not survive a restart.
//
// For every unresolved call this installs a synthetic tool result row and marks
// the call no longer pending, then appends a single cancelled-turn notice when
// any call was closed. The closure is display-layer only; it is re-run (and is
// idempotent) every time a runtime is initialized from the same baseline.
//
// It is meant to run on a freshly built projection with no followers, so the
// appended rows are covered by the next initial snapshot and no first-sequence
// change is needed. Record identity "tool:<callID>" is free because an
// unresolved call has no paired result row by definition.
//
// It returns the number of tool calls closed.
func (p *Projection) RecoverUnresolvedToolCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	resolved := make(map[string]bool)
	for _, row := range p.buffer.messages {
		if row.message.Role == "tool" && row.message.ToolCallID != "" {
			resolved[row.message.ToolCallID] = true
		}
	}

	var closed []Message
	var noticedTurnID string
	seen := make(map[string]bool)
	for _, row := range p.buffer.messages {
		if row.message.Role != "assistant" {
			continue
		}
		for i := range row.message.ToolCalls {
			call := &row.message.ToolCalls[i]
			if call.ID == "" || resolved[call.ID] || seen[call.ID] {
				continue
			}
			seen[call.ID] = true
			call.Pending = false
			closed = append(closed, Message{
				RecordID:   "tool:" + call.ID,
				Role:       "tool",
				TurnID:     row.message.TurnID,
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Content:    interruptedResultText,
				CreatedAt:  time.Now().UnixMilli(),
				Source:     row.message.Source,
			})
			if noticedTurnID == "" {
				noticedTurnID = row.message.TurnID
			}
		}
	}
	if len(closed) == 0 {
		return 0
	}

	published := make([]Message, 0, len(closed)+1)
	for _, message := range closed {
		p.buffer.messages = append(p.buffer.messages, &bufferedMessage{message: message})
		published = append(published, message)
	}

	notice := interruptedNotice(nil)
	notice.RecordID = "terminal:" + noticedTurnID + ":" + notice.Code
	notice.TurnID = noticedTurnID
	p.buffer.messages = append(p.buffer.messages, &bufferedMessage{message: notice})
	published = append(published, notice)

	p.revision++
	p.publishChangeLocked(Change{Records: published})
	return len(closed)
}
