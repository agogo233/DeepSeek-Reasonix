package session

import (
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/transcript"
)

// TestRuntimeRecoversDanglingAskOnOpen reproduces a service restart while the
// model's ask tool was waiting for an answer: the persisted view holds an
// assistant message with an unanswered call and an open turn. Rebuilding the
// runtime must settle the call (synthetic result + notice) and mark the turn
// recovery_required instead of leaving a perpetual unknown card.
func TestRuntimeRecoversDanglingAskOnOpen(t *testing.T) {
	session := &Session{
		id:   "dangling-ask",
		next: 5,
		recentMessages: []provider.Message{
			{ID: "user-1", Role: provider.RoleUser, Content: "commit this work", Origin: provider.MessageOriginUser},
			{ID: "ask-1", Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{
				{ID: "call-ask-1", Name: "ask", Arguments: `{"questions":[{"header":"提交方式"}]}`},
			}},
		},
		projection: Projection{
			TurnID:     "turn-1",
			TurnStatus: event.TurnInProgress,
		},
	}

	runtime, err := newRuntime(SessionRef{HostID: "local", SessionID: session.id}, session)
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := runtime.Transcript().Snapshot(transcript.PageRequest{Records: 32})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Runtime.Status != event.TurnRecoveryRequired {
		t.Fatalf("turn status = %q, want recovery_required", snapshot.Runtime.Status)
	}

	var toolRow, noticeRow bool
	var pending bool
	for _, record := range snapshot.Records {
		row := record.Message
		switch {
		case row.Role == "tool" && row.ToolCallID == "call-ask-1":
			toolRow = true
		case row.Code == "cancelled_turn_display":
			noticeRow = true
		}
		for _, call := range row.ToolCalls {
			if call.ID == "call-ask-1" && call.Pending {
				pending = true
			}
		}
	}
	if !toolRow {
		t.Fatal("recovered runtime left the dangling ask without a synthetic result")
	}
	if !noticeRow {
		t.Fatal("recovered runtime did not surface a cancelled-turn notice")
	}
	if pending {
		t.Fatal("recovered runtime left the ask call pending")
	}
}

// TestRuntimeOpenTurnWithoutDanglingCallStaysRecoverable ensures a session with
// an open turn but no unresolved tool call still marks recovery required while
// adding no synthetic rows.
func TestRuntimeOpenTurnWithoutDanglingCallStaysRecoverable(t *testing.T) {
	session := &Session{
		id:   "open-only",
		next: 3,
		recentMessages: []provider.Message{
			{ID: "user-1", Role: provider.RoleUser, Content: "hello", Origin: provider.MessageOriginUser},
		},
		projection: Projection{TurnID: "turn-1", TurnStatus: event.TurnInProgress},
	}

	runtime, err := newRuntime(SessionRef{HostID: "local", SessionID: session.id}, session)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := runtime.Transcript().Snapshot(transcript.PageRequest{Records: 32})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Runtime.Status != event.TurnRecoveryRequired {
		t.Fatalf("turn status = %q, want recovery_required", snapshot.Runtime.Status)
	}
	for _, record := range snapshot.Records {
		if record.Message.Role == "tool" || record.Message.Code == "cancelled_turn_display" {
			t.Fatalf("open turn without a dangling call added synthetic rows: %+v", record.Message)
		}
	}
}
