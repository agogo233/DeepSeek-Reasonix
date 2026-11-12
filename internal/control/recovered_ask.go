package control

import (
	"context"
	"encoding/json"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/session"
)

// recoveredAsk is a pending ask rebuilt after a restart from an interrupted
// turn's dangling tool call.
type recoveredAsk struct {
	callID    string
	questions []event.AskQuestion
}

// recoveredAskEntry pairs a rebuilt ask's registration ID with its durable
// evidence, for cancellation settlement.
type recoveredAskEntry struct {
	id        string
	callID    string
	questions []event.AskQuestion
}

// RecoverPendingAsk rebuilds the ask an interrupted turn left waiting, so a
// restarting frontend can answer or cancel it instead of staring at a stuck
// session. It is a no-op unless the durable projection shows an interrupted
// turn whose trailing ask tool call never received a result.
func (c *Controller) RecoverPendingAsk() {
	if c == nil {
		return
	}
	_, runtime, exclusive := c.v3Binding()
	if !exclusive || runtime == nil {
		return
	}
	ask, ok := recoverableAskFromProjection(runtime.ExecutionSnapshot().Session.Projection)
	if !ok {
		return
	}
	c.registerRecoveredAsk(ask)
}

// recoverableAskFromProjection detects a pending ask left by an interrupted
// turn. It reads only the durable projection, so repeated opens stay idempotent:
// once the ask is answered the tool call has a recorded result and no longer
// matches.
func recoverableAskFromProjection(projection session.Projection) (recoveredAsk, bool) {
	if !projection.TurnStatus.Terminal() {
		return recoveredAsk{}, false
	}
	call, ok := danglingAskCall(projection.ModelMessages)
	if !ok {
		return recoveredAsk{}, false
	}
	questions, err := agent.ParseAskRequestArguments(json.RawMessage(call.Arguments))
	if err != nil {
		return recoveredAsk{}, false
	}
	return recoveredAsk{callID: call.ID, questions: questions}, true
}

// danglingAskCall returns the most recent assistant ask tool call that has no
// recorded tool result — the question an interrupted turn never answered.
func danglingAskCall(messages []provider.Message) (provider.ToolCall, bool) {
	answered := make(map[string]bool)
	for _, message := range messages {
		if message.Role == provider.RoleTool && message.ToolCallID != "" {
			answered[message.ToolCallID] = true
		}
	}
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.Role != provider.RoleAssistant {
			continue
		}
		for _, call := range message.ToolCalls {
			if call.Name == "ask" && !answered[call.ID] {
				return call, true
			}
		}
	}
	return provider.ToolCall{}, false
}

// registerRecoveredAsk registers the rebuilt ask with the prompt owner and
// approval manager and re-emits its AskRequest so a connected frontend renders
// the card again. It never blocks: no goroutine is waiting on the reply channel.
func (c *Controller) registerRecoveredAsk(ask recoveredAsk) {
	c.approval.promptEmitMu.Lock()
	defer c.approval.promptEmitMu.Unlock()
	if c.approval.hasRecoveredAskForCall(ask.callID) {
		return
	}
	id, _ := c.approval.registerAsk(ask.questions)
	c.approval.markAskRecovered(id, ask.callID)
	c.registerOwnedPrompt(id, PromptAsk)
	turnID, _, _, _ := c.turnEventRuntimeStatus()
	_, runtimeEpoch := c.promptIdentitySnapshot()
	if identity := c.bindOwnedPromptRouting(id, turnID, runtimeEpoch); identity.TurnID != "" {
		turnID = identity.TurnID
	}
	if err := event.EmitChecked(c.sink, event.Event{Kind: event.AskRequest, TurnID: turnID, ItemID: id, Ask: event.Ask{ID: id, Questions: ask.questions, TurnID: turnID}}); err != nil {
		c.cancelOwnedPrompt(id)
		return
	}
	c.approval.markAskEmitted(id)
}

// answerRecoveredAskLocked settles a rebuilt ask once the user answers. There
// is no live goroutine to resume, so the answer is recorded as the tool result
// for the interrupted call instead; a later turn then sees the real answer
// rather than an interruption placeholder.
func (c *Controller) answerRecoveredAskLocked(id string, pending pendingAsk, answers []event.AskAnswer) error {
	if pending.recovered == nil {
		return nil
	}
	if !askAnswersHaveSelection(answers) {
		return c.recordRecoveredToolResult(pending.recovered.callID, pending.questions, nil)
	}
	return c.recordRecoveredToolResult(pending.recovered.callID, pending.questions, answers)
}

// recordRecoveredToolResult appends the ask's settled tool result to the
// durable provider history. It is the only write an answered rebuilt ask needs:
// the interaction was already resolved by the answer transition, and no turn is
// running to resume.
func (c *Controller) recordRecoveredToolResult(callID string, questions []event.AskQuestion, answers []event.AskAnswer) error {
	result := provider.Message{
		Role:       provider.RoleTool,
		ToolCallID: callID,
		Name:       "ask",
		Content:    agent.FormatAskAnswers(questions, answers),
		ID:         agent.NewMessageID(),
	}
	if err := c.RecordSessionMessages(context.Background(), "recovered-ask", []provider.Message{result}); err != nil {
		return err
	}
	if c.executor != nil {
		if sess := c.executor.Session(); sess != nil {
			sess.Add(result)
		}
	}
	if answers == nil {
		c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "The previous question was cancelled because the session restarted. Send a message to continue."})
	} else {
		c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "The previous task was interrupted by the restart; your answer was recorded. Send a message to continue."})
	}
	return nil
}

// hasRecoveredAsks reports whether any rebuilt ask is still pending.
func (c *Controller) hasRecoveredAsks() bool {
	if c == nil {
		return false
	}
	return c.approval.hasAnyRecoveredAsk()
}

// settleRecoveredAsks cancels every rebuilt ask still pending. It is used when
// the user presses stop with no live turn to cancel, and when a new turn
// admission would otherwise leave a stale rebuilt card behind. Claims under the
// approval lock so concurrent settle passes cannot double-record a result.
func (c *Controller) settleRecoveredAsks() {
	if c == nil {
		return
	}
	for _, pending := range c.approval.claimRecoveredAsks() {
		c.settleRecoveredAsk(pending)
	}
}

func (c *Controller) settleRecoveredAsk(p recoveredAskEntry) {
	_ = c.emitTurnEventChecked(event.Event{Kind: event.PromptAnswered, ItemID: p.id, InteractionState: string(PromptCancelled), Status: event.TurnInterrupted})
	c.promptOwner.MarkIDTerminal(p.id, PromptCancelled)
	_ = c.recordRecoveredToolResult(p.callID, p.questions, nil)
}
