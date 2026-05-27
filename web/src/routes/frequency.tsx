import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, type Filter } from "@/api/client";
import { fmtDuration, fmtTokens } from "@/lib/format";

export function FrequencyPage() {
  const [model, setModel] = useState("");
  const [tool, setTool] = useState("");
  const filter = useMemo<Filter>(() => {
    const f: Filter = {};
    if (model) f.model = { in: model.split(",").map((s) => s.trim()) };
    if (tool) f.tool_name = { in: tool.split(",").map((s) => s.trim()) };
    return f;
  }, [model, tool]);
  const { data, isLoading, error } = useQuery({ queryKey: ["freq", filter], queryFn: () => api.frequency(filter) });

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">Frequency</h1>
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
        <input className="input" placeholder="model (csv)" value={model} onChange={(e) => setModel(e.target.value)} />
        <input className="input" placeholder="tool (csv)" value={tool} onChange={(e) => setTool(e.target.value)} />
      </div>
      {error ? <div className="text-red-300">{String(error)}</div> : null}
      {isLoading ? <div className="text-zinc-400">loading…</div> : null}
      <table className="table">
        <thead>
          <tr>
            <th>Model</th>
            <th>Agent</th>
            <th>Tool</th>
            <th>Operation</th>
            <th>Calls</th>
            <th>Error rate</th>
            <th>avg latency</th>
            <th>p95 latency</th>
            <th>avg in / out</th>
          </tr>
        </thead>
        <tbody>
          {data?.rows?.map((r, i) => (
            <tr key={i} className="hover:bg-zinc-900/50">
              <td className="font-mono">{r.model || "—"}</td>
              <td>{r.agentName || "—"}</td>
              <td>{r.toolName || "—"}</td>
              <td>{r.operation || "—"}</td>
              <td>{r.calls}</td>
              <td className={r.errorRate > 0.05 ? "text-red-300" : ""}>{(r.errorRate * 100).toFixed(1)}%</td>
              <td>{fmtDuration(r.avgDurationMs, "ms")}</td>
              <td>{fmtDuration(r.p95DurationMs, "ms")}</td>
              <td>
                {fmtTokens(r.avgInputTokens)} / {fmtTokens(r.avgOutputTokens)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
