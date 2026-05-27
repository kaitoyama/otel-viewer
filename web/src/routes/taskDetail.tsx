import { useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/api/client";
import { fmtDuration, fmtTime, fmtTokens, shortId } from "@/lib/format";

export function TaskDetailPage() {
  const { id } = useParams({ from: "/tasks/$id" });
  const navigate = useNavigate();
  const { data, isLoading, error } = useQuery({ queryKey: ["task", id], queryFn: () => api.getTask(id) });
  const [selected, setSelected] = useState<string[]>([]);

  const byModel = useMemo(() => {
    const map = new Map<string, { runs: number; tokens: number; latencyMs: number[]; feedbackScores: number[] }>();
    data?.runs?.forEach((r) => {
      const k = r.model || "unknown";
      const e = map.get(k) ?? { runs: 0, tokens: 0, latencyMs: [], feedbackScores: [] };
      e.runs++;
      e.tokens += r.totals?.totalTokens ?? 0;
      e.latencyMs.push(new Date(r.endTime).getTime() - new Date(r.startTime).getTime());
      if (r.minFeedback != null) e.feedbackScores.push(r.minFeedback);
      map.set(k, e);
    });
    return Array.from(map.entries());
  }, [data]);

  if (isLoading) return <div className="text-zinc-400">loading…</div>;
  if (error) return <div className="text-red-300">{String(error)}</div>;
  if (!data) return null;

  function toggle(id: string) {
    setSelected((s) => (s.includes(id) ? s.filter((x) => x !== id) : s.length < 4 ? [...s, id] : s));
  }

  return (
    <div className="space-y-5">
      <header className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">
          Task <span className="font-mono">{shortId(id, 32)}</span>
        </h1>
        <button
          className="btn btn-primary"
          disabled={selected.length < 2 || selected.length > 4}
          onClick={() => navigate({ to: "/compare", search: { ids: selected.join(",") } })}
        >
          Compare ({selected.length})
        </button>
      </header>

      <section>
        <h2 className="text-sm uppercase tracking-wide text-zinc-500 mb-2">By model</h2>
        <table className="table">
          <thead>
            <tr>
              <th>Model</th>
              <th>Runs</th>
              <th>Total tokens</th>
              <th>p50 latency</th>
              <th>Avg feedback</th>
            </tr>
          </thead>
          <tbody>
            {byModel.map(([m, e]) => {
              const p50 = e.latencyMs.slice().sort((a, b) => a - b)[Math.floor(e.latencyMs.length / 2)] ?? 0;
              const avgFb = e.feedbackScores.length
                ? (e.feedbackScores.reduce((a, b) => a + b, 0) / e.feedbackScores.length).toFixed(2)
                : "—";
              return (
                <tr key={m}>
                  <td className="font-mono">{m}</td>
                  <td>{e.runs}</td>
                  <td>{fmtTokens(e.tokens)}</td>
                  <td>{fmtDuration(p50, "ms")}</td>
                  <td>{avgFb}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </section>

      <section>
        <h2 className="text-sm uppercase tracking-wide text-zinc-500 mb-2">Runs ({data.runs.length})</h2>
        <table className="table">
          <thead>
            <tr>
              <th></th>
              <th>Trace</th>
              <th>Model</th>
              <th>Steps</th>
              <th>Tokens</th>
              <th>Feedback</th>
              <th>Start</th>
            </tr>
          </thead>
          <tbody>
            {data.runs.map((r) => (
              <tr key={r.traceId} className="hover:bg-zinc-900/50">
                <td>
                  <input type="checkbox" checked={selected.includes(r.traceId)} onChange={() => toggle(r.traceId)} />
                </td>
                <td>
                  <Link to="/traces/$id" params={{ id: r.traceId }} className="font-mono text-emerald-300">
                    {shortId(r.traceId, 12)}
                  </Link>
                </td>
                <td>{r.model}</td>
                <td>{r.stepCount}</td>
                <td>{fmtTokens(r.totals?.totalTokens)}</td>
                <td>{r.minFeedback != null ? r.minFeedback.toFixed(2) : <span className="text-zinc-500">—</span>}</td>
                <td className="text-zinc-400 text-xs">{fmtTime(r.startTime)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
    </div>
  );
}
