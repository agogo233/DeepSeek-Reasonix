// @vitest-environment jsdom
import "./testkit";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { HubPort, SessionSweepReport } from "../port/hub";
import { SessionSweep } from "./SessionSweep";

afterEach(() => { cleanup(); localStorage.clear(); vi.restoreAllMocks(); });

function hub(over: Partial<HubPort> = {}) {
  return {
    sweepSessions: vi.fn().mockResolvedValue({
      matched: 0,
      rows: [],
      pinned: 0,
      running: 0,
      recovered: 0,
      errors: [],
    } as SessionSweepReport),
    removeSessions: vi.fn().mockResolvedValue(undefined),
    ...over,
  } as unknown as HubPort;
}

describe("SessionSweep", () => {
  it("previews matched conversations before anything is deleted", async () => {
    const h = hub({
      sweepSessions: vi.fn().mockResolvedValue({
        matched: 2,
        rows: [
          { path: "/sessions/a.jsonl", title: "Alpha", lastActive: "", archived: false, turns: 3 },
          { path: "/sessions/b.jsonl", title: "Beta", lastActive: "", archived: true, turns: 1 },
        ],
        pinned: 1,
        running: 0,
        recovered: 0,
        errors: [],
      } as SessionSweepReport),
    });
    render(<SessionSweep hub={h} />);
    await userEvent.click(screen.getByRole("button", { name: "预览" }));
    await waitFor(() => expect(screen.getByText("将删除 2 个会话")).toBeTruthy());
    expect(screen.getByText("Alpha")).toBeTruthy();
    expect(screen.getByText("Beta")).toBeTruthy();
    expect(h.removeSessions).not.toHaveBeenCalled();
  });

  it("deletes the previewed rows after confirmation", async () => {
    const h = hub({
      sweepSessions: vi.fn().mockResolvedValue({
        matched: 1,
        rows: [{ path: "/sessions/a.jsonl", title: "Alpha", lastActive: "", archived: false, turns: 3 }],
        pinned: 0,
        running: 0,
        recovered: 0,
        errors: [],
      } as SessionSweepReport),
    });
    render(<SessionSweep hub={h} />);
    await userEvent.click(screen.getByRole("button", { name: "预览" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "删除 1 个会话" })).toBeTruthy());
    await userEvent.click(screen.getByRole("button", { name: "删除 1 个会话" }));
    const dialog = screen.getByRole("alertdialog");
    expect(dialog).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "确认删除" }));
    await waitFor(() => expect(h.removeSessions).toHaveBeenCalledWith(["/sessions/a.jsonl"]));
  });

  it("shows a strong warning before an all sweep is confirmed", async () => {
    const h = hub({
      sweepSessions: vi.fn().mockResolvedValue({
        matched: 1,
        rows: [{ path: "/sessions/a.jsonl", title: "Alpha", lastActive: "", archived: false, turns: 3 }],
        pinned: 0,
        running: 0,
        recovered: 0,
        errors: [],
      } as SessionSweepReport),
    });
    render(<SessionSweep hub={h} />);
    await userEvent.click(screen.getByRole("radio", { name: "全部" }));
    await userEvent.click(screen.getByRole("button", { name: "预览" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "删除 1 个会话" })).toBeTruthy());
    await userEvent.click(screen.getByRole("button", { name: "删除 1 个会话" }));
    expect(screen.getByText("这会删除所有会话，无法恢复。请确认你确实想这么做。")).toBeTruthy();
  });

  it("reports when nothing matched", async () => {
    const h = hub();
    render(<SessionSweep hub={h} />);
    await userEvent.click(screen.getByRole("button", { name: "预览" }));
    await waitFor(() => expect(screen.getByText("没有匹配的会话")).toBeTruthy());
  });
});
