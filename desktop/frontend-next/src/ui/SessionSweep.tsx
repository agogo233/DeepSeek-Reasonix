import { useState } from "react";
import { t } from "../i18n";
import { reason } from "../i18n/kernel";
import type { HubPort, SessionSweepReport, SessionSweepRequest } from "../port/hub";
import { SegChoice, SettingRow } from "./SettingRow";

// How many paths one remove-many request may carry: the kernel bounds a request
// body, so an oversized sweep is split into batches that each land or refuse as
// a whole. A refused batch stops the run and keeps the rest on disk.
const BATCH = 500;

type Mode = "idle" | "empty" | "all";

export function SessionSweep({ hub, onChanged }: { hub: HubPort; onChanged?: () => void }) {
  const [mode, setMode] = useState<Mode>("idle");
  const [days, setDays] = useState(30);
  const [includeArchived, setIncludeArchived] = useState(false);
  const [report, setReport] = useState<SessionSweepReport | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [confirming, setConfirming] = useState(false);

  // A stale preview describes the previous conditions: as soon as any of them
  // changes the report is no longer what the delete button would remove, so it
  // has to go before the caller can act on it.
  const invalidate = () => {
    setReport(null);
    setConfirming(false);
  };

  const req = (): SessionSweepRequest => ({
    olderThanDays: mode === "idle" ? days : 0,
    empty: mode === "empty",
    all: mode === "all",
    includeArchived,
  });

  const preview = async () => {
    setBusy(true);
    setError("");
    setConfirming(false);
    setReport(null);
    try {
      setReport(await hub.sweepSessions(req()));
    } catch (e) {
      setError(reason(e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!report) return;
    setBusy(true);
    setError("");
    setConfirming(false);
    const paths = report.rows.map((row) => row.path);
    let firstError: unknown;
    let deleted = 0;
    for (let i = 0; i < paths.length; i += BATCH) {
      try {
        await hub.removeSessions(paths.slice(i, i + BATCH));
        deleted += Math.min(BATCH, paths.length - i);
      } catch (e) {
        firstError ??= e;
        break;
      }
    }
    // A batch that failed keeps the preview on screen so what is still on disk
    // stays visible and retryable; a fully successful run clears it.
    if (firstError) setError(reason(firstError));
    else setReport(null);
    setBusy(false);
    if (deleted > 0) onChanged?.();
  };

  // Nothing may change the conditions while the confirmation is open: the
  // warning and the rows it stands for belong to the conditions that produced
  // them.
  const locked = busy || confirming;

  const modes: [Mode, string][] = [
    ["idle", t("about.sweep_idle")],
    ["empty", t("about.sweep_empty")],
    ["all", t("about.sweep_all")],
  ];

  return (
    <SettingRow id="session-sweep" title={t("about.sweep_title")} desc={t("about.sweep_desc")}>
      <SegChoice<Mode> label={t("about.sweep_title")} value={mode} disabled={locked} action="sweep.mode"
        onPick={(m) => { setMode(m); invalidate(); }} options={modes} />
      {mode === "idle" && (
        <label className="why">
          {t("about.sweep_days", { n: days })}
          <input type="number" min={1} max={3650} value={days} disabled={locked}
            onChange={(e) => { setDays(Math.max(1, Math.min(3650, Number(e.target.value) || 1))); invalidate(); }} />
        </label>
      )}
      <label className="why">
        <input type="checkbox" checked={includeArchived} disabled={locked}
          onChange={(e) => { setIncludeArchived(e.target.checked); invalidate(); }} />
        {t("about.sweep_include_archived")}
      </label>
      <div>
        <button data-action="sweep.preview" disabled={locked} onClick={() => void preview()}>
          {t("about.sweep_preview")}
        </button>
        {report && report.matched > 0 && (
          <button data-action="sweep.delete" disabled={locked} onClick={() => setConfirming(true)}>
            {t("about.sweep_delete", { n: report.matched })}
          </button>
        )}
      </div>
      {report && report.matched === 0 && <span className="why">{t("about.sweep_none")}</span>}
      {report && report.matched > 0 && (
        <>
          <span className="why">{t("about.sweep_matched", { n: report.matched })}</span>
          <ul>
            {report.rows.slice(0, 50).map((row) => (
              <li key={row.path}>{row.title || row.path}</li>
            ))}
            {report.rows.length > 50 && <li className="why">…</li>}
          </ul>
        </>
      )}
      {report && report.errors.length > 0 && (
        <ul className="why">
          {report.errors.map((e, i) => <li key={i}>{e}</li>)}
        </ul>
      )}
      {error && <span className="why">{error}</span>}
      {confirming && report && (
        <div role="alertdialog" aria-label={t("about.sweep_title")}>
          {mode === "all" && <strong>{t("about.sweep_warning_all")}</strong>}
          <div>
            <button data-action="sweep.confirm" disabled={busy} onClick={() => void remove()}>
              {t("about.sweep_confirm")}
            </button>
            <button disabled={busy} onClick={() => setConfirming(false)}>{t("common.cancel")}</button>
          </div>
        </div>
      )}
    </SettingRow>
  );
}
