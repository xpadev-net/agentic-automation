import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { api, type LogsResponse, type RunDetailResponse } from "./api";
import { stateClass } from "./RunList";

type Item =
  | { kind: "line"; key: number; seq: number; ts?: string; line: string }
  | { kind: "gap"; key: number; missing: number };

function fmtTime(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  return isNaN(d.getTime()) ? iso : d.toLocaleString();
}

function fmtClock(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  return isNaN(d.getTime()) ? "" : d.toLocaleTimeString();
}

export default function RunDetail({ runId }: { runId: number }) {
  const [detail, setDetail] = useState<RunDetailResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [items, setItems] = useState<Item[]>([]);
  const [status, setStatus] = useState("");

  const paneRef = useRef<HTMLDivElement>(null);
  const stickRef = useRef(true);
  const lastSeqRef = useRef(0);
  const keyRef = useRef(0);

  const addEntry = useCallback((e: { seq?: number; ts?: string; line?: string }) => {
    const seq = e.seq ?? lastSeqRef.current + 1;
    const next: Item[] = [];
    if (lastSeqRef.current && seq > lastSeqRef.current + 1) {
      next.push({
        kind: "gap",
        key: ++keyRef.current,
        missing: seq - lastSeqRef.current - 1,
      });
    }
    lastSeqRef.current = Math.max(lastSeqRef.current, seq);
    next.push({ kind: "line", key: ++keyRef.current, seq, ts: e.ts, line: e.line ?? "" });
    setItems((prev) => [...prev, ...next]);
  }, []);

  // Keep the view pinned to the bottom while the user hasn't scrolled up.
  useLayoutEffect(() => {
    const pane = paneRef.current;
    if (pane && stickRef.current) pane.scrollTop = pane.scrollHeight;
  }, [items]);

  useEffect(() => {
    lastSeqRef.current = 0;
    setItems([]);
    setStatus("");
    let alive = true;
    let es: EventSource | null = null;

    (async () => {
      let data: RunDetailResponse | null;
      try {
        data = await api<RunDetailResponse>(`/api/ui/runs/${runId}`);
      } catch (e) {
        if (alive) setError((e as Error).message);
        return;
      }
      if (!alive) return;
      if (data === null) {
        window.location.reload(); // 401 → login
        return;
      }
      setDetail(data);

      // Fast path: REST snapshot, then SSE live tail.
      try {
        const snap = await api<LogsResponse>(`/api/ui/runs/${runId}/logs?limit=1000`);
        if (alive && snap) (snap.entries ?? []).forEach(addEntry);
      } catch {
        /* fall back to SSE replay */
      }
      if (!alive) return;

      es = new EventSource(`/api/ui/runs/${runId}/logs/stream?after_seq=${lastSeqRef.current}`);
      setStatus("live");

      es.addEventListener("message", (ev) => {
        try {
          addEntry(JSON.parse(ev.data) as { seq?: number; ts?: string; line?: string });
        } catch {
          /* ignore malformed events */
        }
      });
      es.addEventListener("end", (ev) => {
        es?.close();
        try {
          const d = JSON.parse(ev.data) as { state?: string };
          setStatus(`run ${d.state ?? "finished"}`);
          if (d.state) {
            setDetail((prev) => (prev ? { ...prev, run: { ...prev.run, state: d.state! } } : prev));
          }
        } catch {
          setStatus("finished");
        }
      });
      es.onerror = () => {
        // EventSource auto-reconnects; Last-Event-ID resumes the stream.
        setStatus((s) => (s.startsWith("run ") || s === "finished" ? s : "reconnecting…"));
      };
    })();

    return () => {
      alive = false;
      es?.close();
    };
  }, [runId, addEntry]);

  if (error) return <p className="empty">{error}</p>;
  if (!detail) return <p className="loading">loading run…</p>;

  const { run, job_name: jobName } = detail;
  return (
    <>
      <p>
        <a href="#/">← runs</a>
      </p>
      <div className="meta">
        <b>#{run.id}</b>{" "}
        <span className={stateClass(run.state)}>{run.state}</span> {run.agent_type} —{" "}
        {run.repo}#{run.issue_number} {run.issue_title} — started {fmtTime(run.created_at)}
        {jobName ? ` — job ${jobName}` : ""}
      </div>
      <div
        className="logpane"
        ref={paneRef}
        onScroll={(e) => {
          const p = e.currentTarget;
          stickRef.current = p.scrollTop + p.clientHeight >= p.scrollHeight - 40;
        }}
      >
        {items.map((it) =>
          it.kind === "gap" ? (
            <div key={it.key} className="log-gap">
              … ({it.missing} lines dropped/missing) …
            </div>
          ) : (
            <div key={it.key} className="log-line">
              <span className="seq">{it.seq}</span>
              <span className="ts">{fmtClock(it.ts)}</span>
              <span className="txt">{it.line}</span>
            </div>
          ),
        )}
      </div>
      <p className="log-status">{status}</p>
    </>
  );
}
