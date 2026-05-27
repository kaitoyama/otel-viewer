import { useMemo, useState } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { api, type Filter } from "@/api/client";
import { fmtDuration, fmtTime, fmtTokens, shortId } from "@/lib/format";

export function TracesPage() {
  const [model, setModel] = useState("");
  const [tool, setTool] = useState("");
  const [outputContains, setOutputContains] = useState("");
  const [maxScore, setMaxScore] = useState<string>("");
  const [selected, setSelected] = useState<string[]>([]);
  const navigate = useNavigate();

  const filter = useMemo<Filter>(() => {
    const f: Filter = {};
    if (model) f.model = { in: model.split(",").map((s) => s.trim()) };
    if (tool) f.tool_name = { in: tool.split(",").map((s) => s.trim()) };
    if (outputContains) f.tool_output_contains = outputContains;
    if (maxScore) f.feedback_score = { lte: Number(maxScore) };
    return f;
  }, [model, tool, outputContains, maxScore]);

  const { data, isLoading, error } = useQuery({
    queryKey: ["traces", filter],
    queryFn: () => api.listTraces(filter),
  });

  function toggle(id: string) {
    setSelected((s) => (s.includes(id) ? s.filter((x) => x !== id) : s.length < 4 ? [...s, id] : s));
  }

  return (
    <div className="space-y-4">
      <header className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Traces</h1>
        <div className="flex items-center gap-2">
          <span className="text-xs text-zinc-400">{selected.length} selected</span>
          <button
            className="btn btn-primary"
            disabled={selected.length < 2 || selected.length > 4}
            onClick={() => navigate({ to: "/compare", search: { ids: selected.join(",") } })}
          >
            Compare ({selected.length})
          </button>
        </div>
      </header>

      <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
        <input className="input" placeholder="model (csv)" value={model} onChange={(e) => setModel(e.target.value)} />
        <input className="input" placeholder="tool (csv)" value={tool} onChange={(e) => setTool(e.target.value)} />
        <input
          className="input"
          placeholder="tool output contains"
          value={outputContains}
          onChange={(e) => setOutputContains(e.target.value)}
        />
        <input
          className="input"
          type="number"
          step="0.1"
          placeholder="feedback score ≤"
          value={maxScore}
          onChange={(e) => setMaxScore(e.target.value)}
        />
      </div>

      {error ? <div className="text-red-300">{String(error)}</div> : null}
      {isLoading ? <div className="text-zinc-400">loading…</div> : null}

      <table className="table">
        <thead>
          <tr>
            <th></th>
            <th>Trace</th>
            <th>Model</th>
            <th>Task</th>
            <th>Steps</th>
            <th>Tokens</th>
            <th>Latency</th>
            <th>Feedback</th>
            <th>Source</th>
            <th>Start</th>
          </tr>
        </thead>
        <tbody>
          {data?.runs?.map((r) => {
            const dur = new Date(r.endTime).getTime() - new Date(r.startTime).getTime();
            return (
              <tr key={r.traceId} className="hover:bg-zinc-900/50">
                <td>
                  <input type="checkbox" checked={selected.includes(r.traceId)} onChange={() => toggle(r.traceId)} />
                </td>
                <td>
                  <Link to="/traces/$id" params={{ id: r.traceId }} className="font-mono text-emerald-300">
                    {shortId(r.traceId, 12)}
                  </Link>
                  {r.replayGroup ? <span className="pill ml-2">shadow</span> : null}
                </td>
                <td>{r.model || <span className="text-zinc-500">—</span>}</td>
                <td className="font-mono text-xs">{shortId(r.taskId, 24)}</td>
                <td>{r.stepCount}</td>
                <td>{fmtTokens(r.totals?.totalTokens)}</td>
                <td>{fmtDuration(dur, "ms")}</td>
                <td>
                  {r.hasFeedback ? (
                    <span className={`pill ${(r.minFeedback ?? 1) < 0.5 ? "border-red-700 bg-red-900/30 text-red-200" : "border-emerald-700 bg-emerald-900/30 text-emerald-200"}`}>
                      {(r.minFeedback ?? 0).toFixed(2)}
                    </span>
                  ) : (
                    <span className="text-zinc-500">—</span>
                  )}
                </td>
                <td className="text-zinc-400">{r.ingestSource}</td>
                <td className="text-zinc-400 text-xs">{fmtTime(r.startTime)}</td>
              </tr>
            );
          })}
          {data?.runs?.length === 0 ? (
            <tr>
              <td colSpan={10} className="py-6 text-center text-zinc-500">
                No traces yet. Push OTLP to /v1/traces, upload a JSONL on /settings/sources, or kick a Tempo backfill.
              </td>
            </tr>
          ) : null}
        </tbody>
      </table>
    </div>
  );
}
