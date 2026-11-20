// Run: tsx src/__tests__/project-tree-window-storage.test.ts
import assert from "node:assert/strict";

// The window cache module reads localStorage lazily on first use, so the
// storage must exist before the module under test is imported below.
const store = new Map<string, string>();
globalThis.localStorage = {
  getItem: (key: string) => store.get(key) ?? null,
  setItem: (key: string, value: string) => { store.set(key, String(value)); },
  removeItem: (key: string) => { store.delete(key); },
  clear: () => store.clear(),
} as unknown as Storage;

const WINDOW_KEY = "reasonix.projectTree.windowLimits.v1";
const groupKey = (project: string, group: string) => `${project}\u001fgroup:${group}`;
store.set(WINDOW_KEY, JSON.stringify({ version: 1, limits: { [groupKey("p1", "bugs")]: 15 } }));

const { forgetProjectTreeWindowLimit, forgetProjectTreeWindowLimits, projectTreeRuntimeWindowLimits, rememberProjectTreeWindowLimit, resetProjectTreeRuntimeWindowLimits } = await import("../lib/projectTreeWindow");

assert.deepEqual(projectTreeRuntimeWindowLimits(), { [groupKey("p1", "bugs")]: 15 }, "expanded windows restore from local storage");

rememberProjectTreeWindowLimit(groupKey("p1", "feature"), 10);
assert.equal(JSON.parse(store.get(WINDOW_KEY) as string).limits[groupKey("p1", "feature")], 10, "widening a window persists immediately");

rememberProjectTreeWindowLimit(groupKey("p1", "feature"), 5);
assert.equal(JSON.parse(store.get(WINDOW_KEY) as string).limits[groupKey("p1", "feature")], undefined, "collapsing back to the default window drops the persisted entry");

forgetProjectTreeWindowLimit(groupKey("p1", "bugs"));
assert.deepEqual(projectTreeRuntimeWindowLimits(), {}, "forgetting the last window empties the persisted cache");

rememberProjectTreeWindowLimit(groupKey("p2", "bugs"), 20);
resetProjectTreeRuntimeWindowLimits("p2");
assert.deepEqual(projectTreeRuntimeWindowLimits(), {}, "resetting a project clears its persisted windows");

rememberProjectTreeWindowLimit(groupKey("p3", "bugs"), 20);
forgetProjectTreeWindowLimits(new Set(["p9"]));
assert.deepEqual(projectTreeRuntimeWindowLimits(), { [groupKey("p3", "bugs")]: 20 }, "forgetting windows of absent projects keeps the rest");
