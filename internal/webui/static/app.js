// Agent Runs WebUI — dependency-free SPA.
// Views: #/ run list, #/runs/<id> detail + live SSE log.
// Auth: aa_session cookie; 401 -> GitHub login link.

const app = document.getElementById("app");
const whoami = document.getElementById("whoami");

let me = null;
let eventSource = null;

function closeStream() {
  if (eventSource) {
    eventSource.close();
    eventSource = null;
  }
}

async function api(path) {
  const res = await fetch(path, { credentials: "same-origin" });
  if (res.status === 401) return null;
  if (!res.ok) throw new Error(`${path}: HTTP ${res.status}`);
  return res.json();
}

function esc(s) {
  const d = document.createElement("div");
  d.textContent = s;
  return d.innerHTML;
}

function fmtTime(iso) {
  if (!iso) return "";
  const d = new Date(iso);
  return isNaN(d) ? iso : d.toLocaleString();
}

function stateClass(s) {
  return `state state-${s || "unknown"}`;
}

// ---------- login ----------

function renderLogin() {
  whoami.innerHTML = "";
  app.innerHTML = `
    <div class="login-box">
      <p>Agent run のログを閲覧するには GitHub でログインしてください。</p>
      <a class="btn" href="/auth/github/login">Sign in with GitHub</a>
    </div>`;
}

// ---------- run list ----------

async function renderRunList() {
  app.innerHTML = '<p class="loading">loading runs…</p>';
  let data;
  try {
    data = await api("/api/ui/runs?limit=100");
  } catch (e) {
    app.innerHTML = `<p class="empty">${esc(e.message)}</p>`;
    return;
  }
  if (!data) return; // 401 already handled by caller redirecting to login

  const runs = data.runs || [];
  if (runs.length === 0) {
    app.innerHTML = '<p class="empty">No agent runs yet.</p>';
    return;
  }
  const rows = runs
    .map(
      (r) => `<tr data-id="${r.id}">
      <td>#${r.id}</td>
      <td><span class="${stateClass(r.state)}">${esc(r.state)}</span></td>
      <td>${esc(r.agent_type || "")}</td>
      <td>${esc(r.repo || "")}#${r.issue_number ?? ""}</td>
      <td>${fmtTime(r.created_at)}</td>
      <td>${esc(r.issue_title || "")}</td>
    </tr>`,
    )
    .join("");
  app.innerHTML = `
    <table class="runs">
      <thead><tr><th>ID</th><th>State</th><th>Agent</th><th>Issue</th><th>Started</th><th>Title</th></tr></thead>
      <tbody>${rows}</tbody>
    </table>`;
  app.querySelectorAll("tr[data-id]").forEach((tr) =>
    tr.addEventListener("click", () => {
      location.hash = `#/runs/${tr.dataset.id}`;
    }),
  );
}

// ---------- run detail + live log ----------

function logLineEl(seq, ts, text) {
  const div = document.createElement("div");
  div.className = "log-line";
  const tsStr = ts ? new Date(ts).toLocaleTimeString() : "";
  div.innerHTML = `<span class="seq">${seq}</span><span class="ts">${esc(tsStr)}</span><span class="txt">${esc(text)}</span>`;
  return div;
}

function gapEl(after, next) {
  const div = document.createElement("div");
  div.className = "log-gap";
  div.textContent = `… (${next - after - 1} lines dropped/missing) …`;
  return div;
}

async function renderRunDetail(id) {
  app.innerHTML = '<p class="loading">loading run…</p>';
  let run;
  try {
    run = await api(`/api/ui/runs/${id}`);
  } catch (e) {
    app.innerHTML = `<p class="empty">${esc(e.message)}</p>`;
    return;
  }
  if (!run) return;

  app.innerHTML = `
    <p><a href="#/">← runs</a></p>
    <div class="meta">
      <b>#${run.id}</b>
      <span class="${stateClass(run.state)}">${esc(run.state)}</span>
      ${esc(run.agent_type || "")} —
      ${esc(run.repo || "")}#${run.issue_number ?? ""} ${esc(run.issue_title || "")} —
      started ${fmtTime(run.created_at)}${run.job_name ? ` — job ${esc(run.job_name)}` : ""}
    </div>
    <div class="logpane" id="logpane"></div>
    <p class="log-status" id="logstatus"></p>`;

  const pane = document.getElementById("logpane");
  const status = document.getElementById("logstatus");
  let lastSeq = 0;
  let ended = false;

  const atBottom = () => pane.scrollTop + pane.clientHeight >= pane.scrollHeight - 40;
  const append = (el) => {
    const stick = atBottom();
    pane.appendChild(el);
    if (stick) pane.scrollTop = pane.scrollHeight;
  };
  const addEntry = (e) => {
    const seq = e.seq ?? ++lastSeq;
    if (lastSeq && seq > lastSeq + 1) append(gapEl(lastSeq, seq));
    lastSeq = Math.max(lastSeq, seq);
    append(logLineEl(seq, e.ts, e.line ?? ""));
  };

  // Fast path: REST snapshot, then SSE live tail.
  try {
    const snap = await api(`/api/ui/runs/${id}/logs?limit=1000`);
    if (snap) (snap.entries || []).forEach(addEntry);
  } catch (_) {
    /* fall back to SSE replay */
  }

  const es = new EventSource(`/api/ui/runs/${id}/logs/stream?after_seq=${lastSeq}`);
  eventSource = es;
  status.textContent = "live";

  es.addEventListener("log", (ev) => {
    try {
      addEntry(JSON.parse(ev.data));
    } catch (_) {}
  });
  es.addEventListener("end", (ev) => {
    ended = true;
    es.close();
    try {
      const d = JSON.parse(ev.data);
      status.textContent = `run ${d.state || "finished"}`;
      const st = document.querySelector(".meta .state");
      if (st && d.state) {
        st.textContent = d.state;
        st.className = stateClass(d.state);
      }
    } catch (_) {
      status.textContent = "finished";
    }
  });
  es.onerror = () => {
    if (ended) return;
    status.textContent = "reconnecting…";
    // EventSource auto-reconnects (Last-Event-ID resumes the stream).
  };
}

// ---------- routing ----------

async function route() {
  closeStream();
  const m = location.hash.match(/^#\/runs\/(\d+)/);
  if (me === null) {
    me = await api("/api/ui/me");
  }
  if (!me) {
    renderLogin();
    return;
  }
  whoami.innerHTML = `${esc(me.login)} — <a href="#" id="logout">logout</a>`;
  document.getElementById("logout").addEventListener("click", async (e) => {
    e.preventDefault();
    closeStream();
    const res = await fetch("/auth/logout", { method: "POST", credentials: "same-origin" });
    if (res.ok) {
      me = null;
      location.hash = "";
      route();
    }
  });
  if (m) await renderRunDetail(m[1]);
  else await renderRunList();
}

window.addEventListener("hashchange", route);
route();
