import { useCallback, useEffect, useState } from "react";
import { api, type Me } from "./api";
import RunList from "./RunList";
import RunDetail from "./RunDetail";

function useHashRoute(): string {
  const [hash, setHash] = useState(window.location.hash);
  useEffect(() => {
    const onChange = () => setHash(window.location.hash);
    window.addEventListener("hashchange", onChange);
    return () => window.removeEventListener("hashchange", onChange);
  }, []);
  return hash;
}

export default function App() {
  const hash = useHashRoute();
  // undefined = loading, null = unauthenticated
  const [me, setMe] = useState<Me | null | undefined>(undefined);

  useEffect(() => {
    let alive = true;
    api<Me>("/api/ui/me").then((m) => {
      if (alive) setMe(m);
    });
    return () => {
      alive = false;
    };
  }, []);

  const logout = useCallback(async () => {
    const res = await fetch("/auth/logout", {
      method: "POST",
      credentials: "same-origin",
    });
    if (res.ok) {
      setMe(null);
      window.location.hash = "";
    }
  }, []);

  const runMatch = hash.match(/^#\/runs\/(\d+)/);

  return (
    <>
      <header>
        <h1>
          <a href="#/">Agent Runs</a>
        </h1>
        <div id="whoami">
          {me ? (
            <>
              {me.login} —{" "}
              <a href="#" onClick={(e) => (e.preventDefault(), void logout())}>
                logout
              </a>
            </>
          ) : null}
        </div>
      </header>
      <main>
        {me === undefined ? (
          <p className="loading">loading…</p>
        ) : me === null ? (
          <div className="login-box">
            <p>Agent run のログを閲覧するには GitHub でログインしてください。</p>
            <a className="btn" href="/auth/github/login">
              Sign in with GitHub
            </a>
          </div>
        ) : runMatch ? (
          <RunDetail key={runMatch[1]} runId={Number(runMatch[1])} />
        ) : (
          <RunList />
        )}
      </main>
    </>
  );
}
