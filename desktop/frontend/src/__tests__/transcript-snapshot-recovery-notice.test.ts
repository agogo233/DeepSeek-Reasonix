// Run: tsx src/__tests__/transcript-snapshot-recovery-notice.test.ts

import { initialState } from "../lib/useController";
import { historyMessagesToItems } from "../lib/historyItems";
import { transcriptSnapshotState } from "../lib/transcriptSnapshotState";
import type { HistoryMessage } from "../lib/types";
import type { TranscriptSnapshot } from "../lib/transcriptProtocol";

type ReducerState = ReturnType<typeof reducer>;

let passed = 0;
let failed = 0;

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

function recoverySnapshot(status: string, messages: HistoryMessage[] = [], noticeCode?: string): TranscriptSnapshot {
  const records = messages.map((message, order) => ({ id: `r${order}`, order, message, refs: [] }));
  if (noticeCode) {
    records.push({ id: "r-notice", order: records.length, message: { role: "notice", content: "notice", code: noticeCode } as unknown as HistoryMessage, refs: [] });
  }
  return {
    protocolVersion: 1,
    snapshotId: "snap-1",
    identity: { sessionId: "s", headId: "h", rewriteEpoch: 0, runtimeEpoch: "epoch-2" },
    projectionRevision: 1,
    coveredThroughSeq: 10,
    records,
    activeRecords: [],
    runtime: { status, turnId: "turn-1", pendingEvents: [] },
    activeAttempts: [],
    before: 0,
    hasOlder: false,
    totalRecords: records.length,
    totalTurns: records.length,
    stale: false,
  } as unknown as TranscriptSnapshot;
}

const noopApply = ((state: ReducerState) => state) as never;
function rebase(state: ReducerState, snap: TranscriptSnapshot): ReducerState {
  return transcriptSnapshotState(state, snap, historyMessagesToItems, noopApply, Date.now());
}
function recoveryNotices(state: ReducerState): number {
  return state.items.filter((item) => item.kind === "notice" && item.code === "cancelled_turn_display").length;
}

console.log("\ntranscript snapshot recovery_required fallback notice");

// A restarted service leaves the turn in recovery_required. When the backend
// did not supply a cancelled-turn notice, the snapshot installs a local one so
// the session does not look permanently stuck.
{
  const state = rebase(initialState, recoverySnapshot("recovery_required"));
  eq(recoveryNotices(state), 1, "recovery_required without a backend notice installs a fallback notice");
  eq(state.running, false, "recovery_required settles the turn (no running indicator)");
}

// The backend already emitted the cancelled-turn notice; the frontend must not
// duplicate it.
{
  const state = rebase(initialState, recoverySnapshot("recovery_required", [], "cancelled_turn_display"));
  eq(recoveryNotices(state), 1, "a backend cancelled-turn notice is not duplicated");
}

// A second snapshot application must not stack a second synthetic notice; the
// guard also scans the accumulated state items.
{
  let state = rebase(initialState, recoverySnapshot("recovery_required"));
  state = rebase(state, recoverySnapshot("recovery_required"));
  eq(recoveryNotices(state), 1, "repeated snapshots do not stack fallback notices");
}

// Non-recovery statuses install no fallback notice.
{
  const state = rebase(initialState, recoverySnapshot("completed"));
  eq(recoveryNotices(state), 0, "a completed turn installs no fallback notice");
}

if (failed > 0) {
  console.error(`\n${failed} check(s) failed`);
  process.exit(1);
}
process.stdout.write(`\nall ${passed} checks passed\n`);
