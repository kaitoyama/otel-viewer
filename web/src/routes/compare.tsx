import { useSearch } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { api, type Run, type AgentStep } from "@/api/client";
import { fmtDuration, fmtTokens, shortId } from "@/lib/format";
import { useMemo, useState } from "react";
import { clsx } from "clsx";

export function ComparePage() {
  const { ids } = useSearch({ from: "/compare" });
  const traceIds = ids ? ids.split(",").filter(Boolean) : [];
  const { data, isLoading, error } = useQuery({
    queryKey: ["compare", traceIds],
    queryFn: () => api.compare(traceIds),
    enabled: traceIds.length >= 2,
  });
  const [openRow, setOpenRow] = useState<number | null>(null);

  const baseSummary = useMemo(() => {
    if (!data?.summary) return null;
    return data.summary;
  }, [data]);

  if (traceIds.length < 2) return <div className="text-zinc-400">Pass 2..4 trace ids via ?ids=a,b,c.</div>;
  if (isLoading) return <div className="text-zinc-400">loading…</div>;
  if (error) return <div className="text-red-300">{String(error)}</div>;
  if (!data) return null;

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">Compare ({data.runs.length} runs)</h1>

      <section className="grid gap-3" style={{ gridTemplateColumns: `repeat(${data.runs.length}, minmax(0,1fr))` }}>
        {data.runs.map((r, i) => {
          const s = baseSummary!;
          const baseLat = s.latencyMs[0] ?? 0;
          const latDelta = i === 0 ? null : s.latencyMs[i] - baseLat;
          const tokDelta = i === 0 ? null : s.totalTokens[i] - (s.totalTokens[0] ?? 0);
          const stepDelta = i === 0 ? null : s.stepCount[i] - (s.stepCount[0] ?? 0);
          return (
            <div key={r.id} className="rounded border border-zinc-800 p-3">
              <div className="font-mono text-sm text-emerald-300">{shortId(r.id, 12)}</div>
              <div className="mt-1 text-xs text-zinc-400">{r.model}</div>
              <Stat label="Steps" v={s.stepCount[i]} delta={stepDelta} />
              <Stat label="Latency" v={fmtDuration(s.latencyMs[i], "ms")} delta={latDelta} suffix="ms" />
              <Stat label="Tokens" v={fmtTokens(s.totalTokens[i])} delta={tokDelta} />
              <Stat label="Avg feedback" v={s.avgFeedbackScore[i].toFixed(2)} delta={null} />
            </div>
          );
        })}
      </section>

      <section>
        <h2 className="text-sm uppercase tracking-wide text-zinc-500 mb-2">Aligned steps</h2>
        <table className="table">
          <thead>
            <tr>
              <th>#</th>
              {data.runs.map((r) => (
                <th key={r.id}>{shortId(r.id, 8)}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {data.alignment.map((row, ri) => (
              <tr
                key={ri}
                className={clsx("cursor-pointer hover:bg-zinc-900/50", openRow === ri && "bg-zinc-900")}
                onClick={() => setOpenRow(openRow === ri ? null : ri)}
              >
                <td className="text-zinc-500">{ri}</td>
                {row.indices.map((idx, ci) => {
                  const run = data.runs[ci];
                  const step = idx >= 0 ? run.steps[idx] : null;
                  return (
                    <td key={ci}>
                      {step ? <StepCell step={step} /> : <span className="text-zinc-600 line-through">—</span>}
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
        {openRow != null && data.alignment[openRow] ? (
          <RowDrawer row={data.alignment[openRow]} runs={data.runs} />
        ) : null}
      </section>
    </div>
  );
}

function StepCell({ step }: { step: AgentStep }) {
  return (
    <div>
      <div className="text-xs font-mono">{step.kind}</div>
      {step.tool?.name ? <div className="text-xs">tool: {step.tool.name}</div> : null}
      {step.agentName ? <div className="text-xs">agent: {step.agentName}</div> : null}
      <div className="text-[10px] text-zinc-500">{fmtDuration(step.duration)}</div>
      {step.statusError ? <div className="text-xs text-red-300">error</div> : null}
    </div>
  );
}

function RowDrawer({ row, runs }: { row: { indices: number[] }; runs: Run[] }) {
  return (
    <div className="mt-3 grid gap-3" style={{ gridTemplateColumns: `repeat(${runs.length}, minmax(0,1fr))` }}>
      {row.indices.map((idx, ci) => {
        const run = runs[ci];
        const step = idx >= 0 ? run.steps[idx] : null;
        if (!step) return <div key={ci} className="rounded border border-dashed border-zinc-800 p-2 text-zinc-600">no step</div>;
        return (
          <div key={ci} className="rounded border border-zinc-800 p-2 space-y-1 text-sm">
            {step.messages?.map((m, mi) => (
              <div key={mi}>
                <span className="text-zinc-500 mr-2">{m.role}:</span>
                <span className="whitespace-pre-wrap">{m.content}</span>
              </div>
            ))}
            {step.output ? <pre className="whitespace-pre-wrap text-zinc-200">{step.output}</pre> : null}
            {step.tool?.arguments ? (
              <pre className="rounded bg-zinc-900 p-2 text-xs overflow-x-auto">args: {step.tool.arguments}</pre>
            ) : null}
            {step.tool?.result ? (
              <pre className="rounded bg-zinc-900 p-2 text-xs overflow-x-auto">result: {step.tool.result}</pre>
            ) : null}
          </div>
        );
      })}
    </div>
  );
}

function Stat({
  label,
  v,
  delta,
  suffix,
}: {
  label: string;
  v: string | number;
  delta: number | null;
  suffix?: string;
}) {
  return (
    <div className="mt-1 text-sm flex justify-between">
      <span className="text-zinc-400">{label}</span>
      <span>
        {v}
        {delta != null && delta !== 0 ? (
          <span className={clsx("ml-1 text-xs", delta > 0 ? "text-red-300" : "text-emerald-300")}>
            {delta > 0 ? "+" : ""}
            {delta}
            {suffix ?? ""}
          </span>
        ) : null}
      </span>
    </div>
  );
}
