package serve

import (
	"testing"

	"reasonix/internal/provider"
)

func TestHistoryMessagesExposeOnlyPendingFinalReadinessRecovery(t *testing.T) {
	marker := provider.Message{
		Role: provider.RoleTool, ToolCallID: provider.LocalOnlyToolID, Name: provider.LocalOnlyToolName, LocalOnly: true,
		FinalReadinessRecovery: &provider.FinalReadinessRecovery{Pending: true, Missing: []string{"verification"}},
	}
	got := historyMessages([]provider.Message{{Role: provider.RoleUser, Content: "fix it"}, marker})
	if len(got) != 2 || got[1].Role != "final_readiness" || len(got[1].Missing) != 1 || got[1].Missing[0] != "verification" {
		t.Fatalf("pending readiness history = %+v", got)
	}
	marker.FinalReadinessRecovery.Pending = false
	got = historyMessages([]provider.Message{{Role: provider.RoleUser, Content: "fix it"}, marker})
	if len(got) != 1 {
		t.Fatalf("consumed readiness marker should be hidden: %+v", got)
	}
}

func TestHistoryMessagesCarriesCreatedAt(t *testing.T) {
	const userAt, assistantAt = int64(1700000000000), int64(1700000001000)
	got := historyMessages([]provider.Message{
		{Role: provider.RoleUser, Content: "hi", CreatedAt: userAt},
		{Role: provider.RoleAssistant, Content: "hello", CreatedAt: assistantAt},
		{Role: provider.RoleAssistant, Content: "legacy"},
	})
	if len(got) != 3 {
		t.Fatalf("historyMessages() = %+v, want 3 entries", got)
	}
	if got[0].CreatedAt != userAt || got[1].CreatedAt != assistantAt {
		t.Fatalf("createdAt not propagated: user=%d assistant=%d", got[0].CreatedAt, got[1].CreatedAt)
	}
	if got[2].CreatedAt != 0 {
		t.Fatalf("message without a timestamp should stay zero, got %d", got[2].CreatedAt)
	}
}
