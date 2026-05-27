import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, getToken, setToken } from "@/api/client";
import { fmtTime, shortId } from "@/lib/format";

export function SettingsPage() {
  const [token, setLocalToken] = useState(getToken());
  const [baseUrl, setBaseUrl] = useState("http://localhost:3200");
  const [traceql, setTraceql] = useState("");
  const [authMode, setAuthMode] = useState<"none" | "bearer" | "basic">("none");
  const [tempoToken, setTempoToken] = useState("");
  const [user, setUser] = useState("");
  const [pass, setPass] = useState("");
  const [tenant, setTenant] = useState("");
  const [hoursAgo, setHoursAgo] = useState(24);
  const queryClient = useQueryClient();

  const { data: jobs, isLoading } = useQuery({
    queryKey: ["backfills"],
    queryFn: () => api.listBackfills(),
    refetchInterval: (q) => (q.state.data?.some((j) => j.status === "running") ? 2000 : false),
  });
  const start = useMutation({
    mutationFn: () =>
      api.startBackfill({
        baseUrl,
        traceql,
        startNs: Date.now() * 1e6 - hoursAgo * 3600 * 1e9,
        endNs: Date.now() * 1e6,
        authMode,
        token: tempoToken,
        username: user,
        password: pass,
        tenantHeader: tenant,
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["backfills"] }),
  });

  function saveToken() {
    setToken(token);
    alert("Saved. Reload to apply.");
  }

  return (
    <div className="space-y-6 max-w-3xl">
      <section className="space-y-2">
        <h1 className="text-xl font-semibold">API token</h1>
        <p className="text-zinc-400 text-sm">
          If the server was started with <code>-token</code>, paste it here. Stored in localStorage.
        </p>
        <div className="flex gap-2">
          <input
            className="input flex-1 font-mono"
            value={token}
            onChange={(e) => setLocalToken(e.target.value)}
            placeholder="Bearer token"
          />
          <button className="btn btn-primary" onClick={saveToken}>
            Save
          </button>
        </div>
      </section>

      <section className="space-y-2">
        <h1 className="text-xl font-semibold">Tempo backfill</h1>
        <p className="text-zinc-400 text-sm">
          Pulls historical traces from a Grafana Tempo into the local DuckDB. Uses{" "}
          <code>/api/search</code> + <code>/api/traces/&#123;id&#125;</code> only (GA endpoints).
        </p>
        <div className="grid gap-2 sm:grid-cols-2">
          <input className="input" placeholder="Tempo base URL" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} />
          <input
            className="input"
            placeholder='TraceQL filter (e.g. { resource.service.name = "agent-app" })'
            value={traceql}
            onChange={(e) => setTraceql(e.target.value)}
          />
          <select className="input" value={authMode} onChange={(e) => setAuthMode(e.target.value as never)}>
            <option value="none">no auth</option>
            <option value="bearer">bearer</option>
            <option value="basic">basic</option>
          </select>
          {authMode === "bearer" ? (
            <input className="input" placeholder="bearer token" value={tempoToken} onChange={(e) => setTempoToken(e.target.value)} />
          ) : null}
          {authMode === "basic" ? (
            <>
              <input className="input" placeholder="username" value={user} onChange={(e) => setUser(e.target.value)} />
              <input className="input" placeholder="password" type="password" value={pass} onChange={(e) => setPass(e.target.value)} />
            </>
          ) : null}
          <input className="input" placeholder="X-Scope-OrgID (optional)" value={tenant} onChange={(e) => setTenant(e.target.value)} />
          <input
            className="input"
            type="number"
            min={1}
            placeholder="hours back"
            value={hoursAgo}
            onChange={(e) => setHoursAgo(Number(e.target.value))}
          />
        </div>
        <button className="btn btn-primary" onClick={() => start.mutate()} disabled={start.isPending}>
          Start backfill
        </button>
        {start.error ? <div className="text-red-300 text-sm">{String(start.error)}</div> : null}

        <h2 className="text-sm uppercase tracking-wide text-zinc-500 mt-4 mb-1">Jobs</h2>
        {isLoading ? <div className="text-zinc-400">loading…</div> : null}
        <table className="table">
          <thead>
            <tr>
              <th>ID</th>
              <th>Base URL</th>
              <th>Status</th>
              <th>Progress</th>
              <th>Started</th>
              <th>Error</th>
            </tr>
          </thead>
          <tbody>
            {jobs?.map((j) => (
              <tr key={j.id}>
                <td className="font-mono text-xs">{shortId(j.id, 8)}</td>
                <td>{j.baseUrl}</td>
                <td>{j.status}</td>
                <td>
                  {j.fetched}/{j.total}
                </td>
                <td className="text-xs text-zinc-400">{fmtTime(j.startedAt)}</td>
                <td className="text-xs text-red-300">{j.error}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      <section className="space-y-2">
        <h1 className="text-xl font-semibold">JSONL upload</h1>
        <p className="text-zinc-400 text-sm">
          Drop an OTel Collector file-exporter output (NDJSON). Each line is a TracesData object.
        </p>
        <UploadForm />
      </section>
    </div>
  );
}

function UploadForm() {
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  async function onSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    setBusy(true);
    setMsg("");
    try {
      const res = await fetch("/api/traces/upload", { method: "POST", body: form });
      const j = await res.json();
      setMsg(res.ok ? `Ingested ${j.ingested}` : `Error: ${j.error || res.statusText}`);
    } catch (err) {
      setMsg(String(err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <form className="flex items-center gap-2" onSubmit={onSubmit}>
      <input className="input" type="file" name="file" accept=".json,.jsonl,.ndjson" required />
      <button className="btn btn-primary" disabled={busy}>
        Upload
      </button>
      {msg ? <span className="text-sm text-zinc-300">{msg}</span> : null}
    </form>
  );
}
