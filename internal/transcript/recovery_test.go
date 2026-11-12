package transcript

import (
	"testing"
)

// danglingCallBaseline models the persisted view of a turn the service
// restarted while waiting for the model's ask tool answer: the assistant
// message carries the call, but no tool result ever committed.
func danglingCallBaseline() []Message {
	return []Message{
		{RecordID: "m:user-1", Role: "user", MessageID: "user-1", Content: "commit this work", HistoryTurn: 1},
		{RecordID: "m:ask", Role: "assistant", MessageID: "ask", TurnID: "turn-1", Content: "", ToolCalls: []ToolCall{
			{ID: "call-ask-1", Name: "ask", Arguments: `{"questions":[{"header":"提交方式"}]}`, Pending: true},
		}},
	}
}

func TestRecoverUnresolvedToolCallsClosesDanglingCall(t *testing.T) {
	p, err := NewProjection(testIdentity, danglingCallBaseline(), 0)
	if err != nil {
		t.Fatal(err)
	}

	closed := p.RecoverUnresolvedToolCalls()
	if closed != 1 {
		t.Fatalf("closed = %d, want 1", closed)
	}

	rows := snapshot(t, p).Records
	var toolRow, noticeRow *Message
	var pending bool
	for i := range rows {
		row := &rows[i].Message
		switch {
		case row.Role == "tool" && row.ToolCallID == "call-ask-1":
			toolRow = row
		case row.Code == "cancelled_turn_display":
			noticeRow = row
		}
		for _, call := range rows[i].Message.ToolCalls {
			if call.ID == "call-ask-1" && call.Pending {
				pending = true
			}
		}
	}
	if toolRow == nil {
		t.Fatal("synthetic tool result row missing")
	}
	if toolRow.Content == "" || toolRow.ToolName != "ask" || toolRow.RecordID != "tool:call-ask-1" {
		t.Fatalf("synthetic tool row malformed: %+v", toolRow)
	}
	if noticeRow == nil {
		t.Fatal("cancelled-turn notice missing")
	}
	if noticeRow.TurnID != "turn-1" || noticeRow.RecordID != "terminal:turn-1:"+noticeRow.Code {
		t.Fatalf("cancelled-turn notice identity malformed: %+v", noticeRow)
	}
	if pending {
		t.Fatal("dangling call still pending after recovery")
	}
}

func TestRecoverUnresolvedToolCallsNoOpForHealthyHistory(t *testing.T) {
	baseline := []Message{
		{RecordID: "m:user-1", Role: "user", MessageID: "user-1", Content: "question", HistoryTurn: 1},
		{RecordID: "m:ask", Role: "assistant", MessageID: "ask", TurnID: "turn-1", ToolCalls: []ToolCall{{ID: "call-1", Name: "ask"}}},
		{RecordID: "tool:call-1", Role: "tool", ToolCallID: "call-1", ToolName: "ask", Content: "answered"},
	}
	p, err := NewProjection(testIdentity, baseline, 0)
	if err != nil {
		t.Fatal(err)
	}
	if closed := p.RecoverUnresolvedToolCalls(); closed != 0 {
		t.Fatalf("healthy history closed %d calls, want 0", closed)
	}
	if got := len(snapshot(t, p).Records); got != len(baseline) {
		t.Fatalf("records changed on healthy history: %d, want %d", got, len(baseline))
	}
}

func TestRecoverUnresolvedToolCallsIdempotent(t *testing.T) {
	p, err := NewProjection(testIdentity, danglingCallBaseline(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if closed := p.RecoverUnresolvedToolCalls(); closed != 1 {
		t.Fatalf("first pass closed %d, want 1", closed)
	}
	if closed := p.RecoverUnresolvedToolCalls(); closed != 0 {
		t.Fatalf("second pass closed %d, want 0", closed)
	}
	toolRows := 0
	for _, row := range snapshot(t, p).Records {
		if row.Message.Role == "tool" && row.Message.ToolCallID == "call-ask-1" {
			toolRows++
		}
	}
	if toolRows != 1 {
		t.Fatalf("synthetic tool rows = %d, want 1", toolRows)
	}
}
