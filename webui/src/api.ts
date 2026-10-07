export interface Me {
  login: string;
}

export interface RunSummary {
  id: number;
  repo: string;
  issue_number: number;
  issue_title: string;
  pr_id?: number;
  state: string;
  agent_type: string;
  execution_mode: string;
  retry_count: number;
  error_message?: string;
  started_at?: string;
  completed_at?: string;
  created_at: string;
}

export interface RunListResponse {
  runs: RunSummary[];
  limit: number;
  offset: number;
}

export interface RunDetailResponse {
  run: RunSummary;
  logs_count: number;
  job_name?: string;
}

export interface LogEntry {
  seq: number;
  ts?: string;
  line: string;
}

export interface LogsResponse {
  entries: LogEntry[];
  source: string;
  has_more: boolean;
}

// api returns null on 401 (caller shows the login view).
export async function api<T>(path: string): Promise<T | null> {
  const res = await fetch(path, { credentials: "same-origin" });
  if (res.status === 401) return null;
  if (!res.ok) throw new Error(`${path}: HTTP ${res.status}`);
  return (await res.json()) as T;
}
