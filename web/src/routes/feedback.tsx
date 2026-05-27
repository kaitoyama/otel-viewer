import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/api/client";
import { fmtTime, shortId } from "@/lib/format";

export function FeedbackPage() {
  const [max, setMax] = useState(0.5);
  const { data, isLoading, error } = useQuery({
    queryKey: ["feedback-triage", max],
    queryFn: () => api.listFeedback(undefined, max),
  });
  return (
    <div className="space-y-4">
      <header className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Feedback triage</h1>
        <label className="text-sm text-zinc-400 flex items-center gap-2">
          score ≤
          <input
            className="input w-20"
            type="number"
            step="0.1"
            min={0}
            max={1}
            value={max}
            onChange={(e) => setMax(Number(e.target.value))}
          />
        </label>
      </header>
      {error ? <div className="text-red-300">{String(error)}</div> : null}
      {isLoading ? <div className="text-zinc-400">loading…</div> : null}
      <table className="table">
        <thead>
          <tr>
            <th>Trace</th>
            <th>Score</th>
            <th>Label</th>
            <th>Comment</th>
            <th>Created</th>
          </tr>
        </thead>
        <tbody>
          {data?.feedback?.map((fb) => (
            <tr key={fb.id} className="hover:bg-zinc-900/50">
              <td>
                <Link to="/traces/$id" params={{ id: fb.traceId }} className="font-mono text-emerald-300">
                  {shortId(fb.traceId, 14)}
                </Link>
              </td>
              <td>{fb.score.toFixed(2)}</td>
              <td>{fb.label ?? "—"}</td>
              <td>{fb.comment ?? ""}</td>
              <td className="text-xs text-zinc-400">{fmtTime(fb.createdAt)}</td>
            </tr>
          ))}
          {data?.feedback?.length === 0 ? (
            <tr>
              <td colSpan={5} className="py-6 text-center text-zinc-500">
                No feedback at this threshold.
              </td>
            </tr>
          ) : null}
        </tbody>
      </table>
    </div>
  );
}
