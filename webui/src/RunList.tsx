import { useEffect, useState } from "react";
import { api, type RunListResponse, type RunSummary } from "./api";

function fmtTime(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  return isNaN(d.getTime()) ? iso : d.toLocaleString();
}

export function stateClass(s: string): string {
  return `state state-${s || "unknown"}`;
}

export default function RunList() {
  const [runs, setRuns] = useState<RunSummary[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    api<RunListResponse>("/api/ui/runs?limit=100")
      .then((data) => {
        if (!alive) return;
        if (data === null) {
          // 401: the session expired after /api/ui/me — reload shows login.
          window.location.reload();
          return;
        }
        setRuns(data.runs ?? []);
      })
      .catch((e: Error) => {
        if (alive) setError(e.message);
      });
    return () => {
      alive = false;
    };
  }, []);

  if (error) return <p className="empty">{error}</p>;
  if (runs === null) return <p className="loading">loading runs…</p>;
  if (runs.length === 0) return <p className="empty">No agent runs yet.</p>;

  return (
    <table className="runs">
      <thead>
        <tr>
          <th>ID</th>
          <th>State</th>
          <th>Agent</th>
          <th>Issue</th>
          <th>Started</th>
          <th>Title</th>
        </tr>
      </thead>
      <tbody>
        {runs.map((r) => (
          <tr
            key={r.id}
            onClick={() => {
              window.location.hash = `#/runs/${r.id}`;
            }}
          >
            <td>#{r.id}</td>
            <td>
              <span className={stateClass(r.state)}>{r.state}</span>
            </td>
            <td>{r.agent_type}</td>
            <td>
              {r.repo}#{r.issue_number}
            </td>
            <td>{fmtTime(r.created_at)}</td>
            <td>{r.issue_title}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
