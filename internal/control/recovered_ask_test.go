package control

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/session"
)

// newRecoveredAskTestController builds a controller with a live executor session
// so recovered-ask settlement can assert the durable tool result lands in the
// provider history.
func newRecoveredAskTestController(t *testing.T, sink event.Sink) *Controller {
	t.Helper()
	return newOwnedTestController(t, Options{
		Executor: agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard),
		Sink:     sink,
	})
}

func TestDanglingAskCallDetectsUnansweredAsk(t *testing.T) {
	messages := []provider.Message{
		{Role: provider.RoleUser, Content: "go"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "ask-1", Name: "ask", Arguments: `{"questions":[{"question":"Which?","header":"Pick","options":[{"label":"A"},{"label":"B"}]}]}`}}},
	}
	call, ok := danglingAskCall(messages)
	if !ok || call.ID != "ask-1" || call.Name != "ask" {
		t.Fatalf("danglingAskCall = %+v,%v, want ask-1/ask,true", call, ok)
	}
}

func TestDanglingAskCallSkipsAnsweredAsk(t *testing.T) {
	messages := []provider.Message{
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "ask-1", Name: "ask", Arguments: "{}"}}},
		{Role: provider.RoleTool, ToolCallID: "ask-1", Name: "ask", Content: "answered"},
	}
	if _, ok := danglingAskCall(messages); ok {
		t.Fatal("danglingAskCall matched an already-answered ask")
	}
}

func TestDanglingAskCallIgnoresOtherTools(t *testing.T) {
	messages := []provider.Message{
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "t-1", Name: "bash", Arguments: "{}"}}},
	}
	if _, ok := danglingAskCall(messages); ok {
		t.Fatal("danglingAskCall matched a non-ask tool")
	}
}

func TestRecoverableAskFromProjection(t *testing.T) {
	projection := session.Projection{
		TurnStatus: event.TurnInterrupted,
		ModelMessages: []provider.Message{
			{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "ask-1", Name: "ask", Arguments: `{"questions":[{"question":"Which?","header":"Pick","options":[{"label":"A"},{"label":"B"}]}]}`}}},
		},
	}
	ask, ok := recoverableAskFromProjection(projection)
	if !ok {
		t.Fatal("recoverableAskFromProjection did not detect the pending ask")
	}
	if ask.callID != "ask-1" || len(ask.questions) != 1 || ask.questions[0].Prompt != "Which?" {
		t.Fatalf("recovered ask = %+v, want ask-1 / one question / Which?", ask)
	}
}

func TestRecoverableAskFromProjectionSkipsAnsweredTurn(t *testing.T) {
	projection := session.Projection{
		TurnStatus: event.TurnInterrupted,
		ModelMessages: []provider.Message{
			{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "ask-1", Name: "ask", Arguments: `{"questions":[{"question":"Which?","header":"Pick","options":[{"label":"A"},{"label":"B"}]}]}`}}},
			{Role: provider.RoleTool, ToolCallID: "ask-1", Name: "ask", Content: "answered"},
		},
	}
	if _, ok := recoverableAskFromProjection(projection); ok {
		t.Fatal("recoverableAskFromProjection matched an already-answered ask")
	}
}

func TestRecoverableAskFromProjectionSkipsRunningTurn(t *testing.T) {
	projection := session.Projection{
		TurnStatus: event.TurnInProgress,
		ModelMessages: []provider.Message{
			{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "ask-1", Name: "ask", Arguments: "{}"}}},
		},
	}
	if _, ok := recoverableAskFromProjection(projection); ok {
		t.Fatal("recoverableAskFromProjection matched a running turn")
	}
}

// TestAnswerRecoveredAskSettlesWithToolResult proves answering a rebuilt ask
// records the tool result instead of resuming a dead turn.
func TestAnswerRecoveredAskSettlesWithToolResult(t *testing.T) {
	asks := make(chan event.Ask, 4)
	c := newRecoveredAskTestController(t, event.FuncSink(func(e event.Event) {
		if e.Kind == event.AskRequest {
			asks <- e.Ask
		}
	}))

	c.registerRecoveredAsk(recoveredAsk{
		callID: "ask-1",
		questions: []event.AskQuestion{{
			ID: "q1", Header: "Pick", Prompt: "Which option?",
			Options: []event.AskOption{{Label: "A"}, {Label: "B"}},
		}},
	})

	first := <-asks
	if first.ID == "" || first.Questions[0].Prompt != "Which option?" {
		t.Fatalf("recovered AskRequest = %+v, want non-empty id and question", first)
	}

	if err := c.AnswerQuestionChecked(first.ID, []event.AskAnswer{{QuestionID: "q1", Selected: []string{"A"}}}); err != nil {
		t.Fatalf("AnswerQuestionChecked: %v", err)
	}

	history := c.executor.Session().Snapshot()
	if len(history) == 0 {
		t.Fatal("executor history is empty after answering a recovered ask")
	}
	last := history[len(history)-1]
	if last.Role != provider.RoleTool || last.ToolCallID != "ask-1" || last.Name != "ask" {
		t.Fatalf("settled tool result = %+v, want RoleTool / ask-1 / ask", last)
	}
	if !strings.Contains(last.Content, "The user answered") {
		t.Fatalf("tool result content = %q, want user answer summary", last.Content)
	}
}

// TestSettleRecoveredAsksCancelsRebuiltCard proves the cancel path drops the
// rebuilt ask and records a cancelled tool result.
func TestSettleRecoveredAsksCancelsRebuiltCard(t *testing.T) {
	c := newRecoveredAskTestController(t, event.Discard)

	c.registerRecoveredAsk(recoveredAsk{
		callID: "ask-1",
		questions: []event.AskQuestion{{
			ID: "q1", Header: "Pick", Prompt: "Which option?",
			Options: []event.AskOption{{Label: "A"}, {Label: "B"}},
		}},
	})
	if !c.hasRecoveredAsks() {
		t.Fatal("recovered ask was not registered")
	}

	c.settleRecoveredAsks()
	if c.hasRecoveredAsks() {
		t.Fatal("settleRecoveredAsks left a recovered ask registered")
	}
	history := c.executor.Session().Snapshot()
	if len(history) == 0 {
		t.Fatal("executor history is empty after settling a recovered ask")
	}
	last := history[len(history)-1]
	if last.Role != provider.RoleTool || last.ToolCallID != "ask-1" {
		t.Fatalf("settled cancelled result = %+v, want RoleTool / ask-1", last)
	}
}

// TestRecoverPendingAskNoOpWithoutExclusiveRuntime proves the entry point stays
// silent on controllers without a v3 runtime (legacy/CLI paths).
func TestRecoverPendingAskNoOpWithoutExclusiveRuntime(t *testing.T) {
	var count int
	c := newOwnedTestController(t, Options{Sink: event.FuncSink(func(e event.Event) {
		if e.Kind == event.AskRequest {
			count++
		}
	})})
	c.RecoverPendingAsk()
	if count != 0 {
		t.Fatalf("RecoverPendingAsk emitted %d ask requests with no exclusive runtime", count)
	}
}

func TestRecoverPendingAskIdempotentAcrossRepeatedCall(t *testing.T) {
	c := newRecoveredAskTestController(t, event.Discard)
	c.registerRecoveredAsk(recoveredAsk{
		callID: "ask-1",
		questions: []event.AskQuestion{{
			ID: "q1", Header: "Pick", Prompt: "Which option?",
			Options: []event.AskOption{{Label: "A"}, {Label: "B"}},
		}},
	})
	// A second recovery pass must not stack a second card for the same call.
	c.registerRecoveredAsk(recoveredAsk{
		callID: "ask-1",
		questions: []event.AskQuestion{{
			ID: "q1", Header: "Pick", Prompt: "Which option?",
			Options: []event.AskOption{{Label: "A"}, {Label: "B"}},
		}},
	})
	if !c.hasRecoveredAsks() {
		t.Fatal("recovered ask was not registered idempotently")
	}
	if got := len(c.approval.claimRecoveredAsks()); got != 1 {
		t.Fatalf("recovered ask count = %d, want exactly 1 after repeat register", got)
	}
}

func TestDanglingAskCallIgnoresEmptyHistory(t *testing.T) {
	if _, ok := danglingAskCall(nil); ok {
		t.Fatal("danglingAskCall matched empty history")
	}
}

// TestRecoverPendingAskFromPersistedProjectionAndAnswer is the end-to-end shape:
// a process died while waiting on an ask; a fresh service reopens the session and
// rebuilds the ask from the durable projection; answering records the real tool
// result into the provider history instead of resuming a dead turn.
func TestRecoverPendingAskFromPersistedProjectionAndAnswer(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions-v4")
	service, err := session.NewService("serve-test", session.NewFilesystemPersistence(root))
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.Create(t.Context(), session.CreateOptions{SessionID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	askMsg := provider.Message{ID: "assistant-1", Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{
		ID: "ask-1", Name: "ask",
		Arguments: `{"questions":[{"question":"Which?","header":"Pick","options":[{"label":"A"},{"label":"B"}]}]}`,
	}}}
	msgPayload, _ := json.Marshal(map[string]any{"message": askMsg})
	seed := []session.Event{
		{Kind: "turn/start", Payload: json.RawMessage(`{"status":"in_progress"}`)},
		{Kind: "message/complete", Payload: msgPayload},
		{Kind: "turn/end", Payload: json.RawMessage(`{"status":"interrupted"}`)},
	}
	if _, err := target.Session().Append(t.Context(), session.Batch{OperationID: "seed-ask", TurnID: "turn-1", Events: seed}); err != nil {
		t.Fatal(err)
	}
	if _, err := target.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(t.Context(), target.Ref()); err != nil {
		t.Fatal(err)
	}

	// Restart: a fresh service over the same root with a fresh controller.
	restarted, err := session.NewService("serve-test", session.NewFilesystemPersistence(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Shutdown(t.Context()) })

	asks := make(chan event.Ask, 4)
	ctrl := newOwnedTestController(t, Options{
		Executor:         agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard),
		SessionDir:       t.TempDir(),
		SessionService:   restarted,
		ExclusiveSession: true,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.AskRequest {
				asks <- e.Ask
			}
		}),
	})
	if _, err := ctrl.OpenSession(t.Context(), session.SessionRef{HostID: "serve-test", SessionID: "target"}); err != nil {
		t.Fatal(err)
	}
	ctrl.RecoverPendingAsk()

	var ask event.Ask
	select {
	case ask = <-asks:
	default:
		t.Fatal("RecoverPendingAsk did not re-emit the interrupted ask")
	}
	if ask.Questions[0].Prompt != "Which?" || ask.Questions[0].Options[0].Label != "A" {
		t.Fatalf("rebuilt ask = %+v, want Which? / A", ask)
	}

	if err := ctrl.AnswerQuestionChecked(ask.ID, []event.AskAnswer{{QuestionID: "q1", Selected: []string{"A"}}}); err != nil {
		t.Fatalf("AnswerQuestionChecked: %v", err)
	}

	_, runtime, exclusive := ctrl.v3Binding()
	if !exclusive || runtime == nil {
		t.Fatal("v3 exclusive runtime unavailable after reopen")
	}
	projection := runtime.ExecutionSnapshot().Session.Projection
	if len(projection.ModelMessages) == 0 {
		t.Fatal("provider projection is empty after answering")
	}
	last := projection.ModelMessages[len(projection.ModelMessages)-1]
	if last.Role != provider.RoleTool || last.ToolCallID != "ask-1" || last.Name != "ask" {
		t.Fatalf("persisted tool result = %+v, want RoleTool / ask-1 / ask", last)
	}
	if !strings.Contains(last.Content, "The user answered") {
		t.Fatalf("persisted tool result content = %q, want user answer summary", last.Content)
	}
}

// TestAdmissionSettlesRecoveredAskWithoutBreakingNewTurn is the P0 regression:
// a new turn must settle a lingering rebuilt ask before it begins, so the ask's
// cancellation never marks the fresh turn terminal and drops its events.
func TestAdmissionSettlesRecoveredAskWithoutBreakingNewTurn(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions-v4")
	service, err := session.NewService("serve-test", session.NewFilesystemPersistence(root))
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.Create(t.Context(), session.CreateOptions{SessionID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	askMsg := provider.Message{ID: "assistant-1", Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{
		ID: "ask-1", Name: "ask",
		Arguments: `{"questions":[{"question":"Which?","header":"Pick","options":[{"label":"A"},{"label":"B"}]}]}`,
	}}}
	msgPayload, _ := json.Marshal(map[string]any{"message": askMsg})
	seed := []session.Event{
		{Kind: "turn/start", Payload: json.RawMessage(`{"status":"in_progress"}`)},
		{Kind: "message/complete", Payload: msgPayload},
		{Kind: "turn/end", Payload: json.RawMessage(`{"status":"interrupted"}`)},
	}
	if _, err := target.Session().Append(t.Context(), session.Batch{OperationID: "seed-ask", TurnID: "turn-1", Events: seed}); err != nil {
		t.Fatal(err)
	}
	if _, err := target.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(t.Context(), target.Ref()); err != nil {
		t.Fatal(err)
	}

	restarted, err := session.NewService("serve-test", session.NewFilesystemPersistence(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Shutdown(t.Context()) })

	ctrl := newOwnedTestController(t, Options{
		Executor:         agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard),
		SessionDir:       t.TempDir(),
		SessionService:   restarted,
		ExclusiveSession: true,
		Sink:             event.Discard,
	})
	if _, err := ctrl.OpenSession(t.Context(), session.SessionRef{HostID: "serve-test", SessionID: "target"}); err != nil {
		t.Fatal(err)
	}
	ctrl.RecoverPendingAsk()
	if !ctrl.hasRecoveredAsks() {
		t.Fatal("recovered ask was not rebuilt")
	}

	admit := ctrl.prepareTurnAdmissionWithGoalRound(t.Context(), func(context.Context) error { return nil }, nil)
	if err := admit(t.Context()); err != nil {
		t.Fatalf("turn admission failed: %v", err)
	}

	if ctrl.hasRecoveredAsks() {
		t.Fatal("recovered ask survived the new turn admission")
	}
	ledger := ctrl.turnEventLedger()
	if ledger == nil {
		t.Fatal("turn ledger unavailable")
	}
	if status := ledger.CurrentStatus(); status.Terminal() {
		t.Fatalf("new turn marked terminal by recovered-ask settlement: %s", status)
	}
}
