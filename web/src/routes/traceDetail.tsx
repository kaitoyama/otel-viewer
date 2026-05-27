import { useState } from "react";
import { useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/client";
import { fmtDuration, fmtTime, fmtTokens, shortId } from "@/lib/format";

export function TraceDetailPage() {
  const { id } = useParams({ from: "/traces/$id" });
  const queryClient = useQueryClient();
  const { data: run, isLoading, error } = useQuery({ queryKey: ["trace", id], queryFn: () => api.getTrace(id) });
  const [feedbackOpen, setFeedbackOpen] = useState(false);
  const [score, setScore] = useState(0);
  const [label, setLabel] = useState("thumbs_down");
  const [comment, setComment] = useState("");

  const sendFeedback = useMutation({
    mutationFn: () => api.postFeedback({ traceId: id, source: "user", score, label, comment }),
    onSuccess: () => {
      setFeedbackOpen(false);
      setComment("");
      queryClient.invalidateQueries({ queryKey: ["trace", id] });
    },
  });

  if (isLoading) return <div className="text-zinc-400">loading…</div>;
  if (error) return <div className="text-red-300">{String(error)}</div>;
  if (!run) return null;

  return (
    <div className="space-y-4">
      <header className="flex flex-wrap items-baseline justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold font-mono">{shortId(run.id, 16)}</h1>
          <div className="mt-1 flex flex-wrap gap-1.5 text-xs text-zinc-400">
            <span className="pill">{run.model}</span>
            <span className="pill">{run.vendor}</span>
            <span className="pill">task: {shortId(run.taskId, 18)}</span>
            <span className="pill">tokens {fmtTokens(run.totals.totalTokens)}</span>
            <span className="pill">{fmtTime(run.startTime)}</span>
            {run.replayOf ? <span className="pill border-purple-700 bg-purple-900/30 text-purple-200">replay of {shortId(run.replayOf, 8)}</span> : null}
          </div>
        </div>
        <div className="flex gap-2">
          <button className="btn" onClick={() => setFeedbackOpen((v) => !v)}>
            Add feedback
          </button>
        </div>
      </header>

      {feedbackOpen ? (
        <div className="rounded border border-zinc-800 p-3 grid grid-cols-1 sm:grid-cols-[auto_auto_1fr_auto] gap-2 items-center">
          <input
            className="input w-20"
            type="number"
            step="0.1"
            min={0}
            max={1}
            value={score}
            onChange={(e) => setScore(Number(e.target.value))}
          />
          <select className="input" value={label} onChange={(e) => setLabel(e.target.value)}>
            <option value="thumbs_up">thumbs_up</option>
            <option value="thumbs_down">thumbs_down</option>
            <option value="needs_review">needs_review</option>
          </select>
          <input className="input" placeholder="comment" value={comment} onChange={(e) => setComment(e.target.value)} />
          <button className="btn btn-primary" onClick={() => sendFeedback.mutate()} disabled={sendFeedback.isPending}>
            Save
          </button>
        </div>
      ) : null}

      <section className="space-y-2">
        <h2 className="text-sm uppercase tracking-wide text-zinc-500">Steps</h2>
        <div className="space-y-2">
          {run.steps.map((s) => (
            <article key={s.spanId} className="rounded border border-zinc-800 p-3 bg-zinc-950/60">
              <header className="flex items-center justify-between text-sm">
                <div className="flex items-center gap-2">
                  <span className="font-mono text-zinc-400">#{s.index}</span>
                  <span className="pill border-emerald-700 bg-emerald-900/20 text-emerald-200">{s.kind}</span>
                  {s.tool?.name ? <span className="pill">tool: {s.tool.name}</span> : null}
                  {s.agentName ? <span className="pill">agent: {s.agentName}</span> : null}
                  {s.operation ? <span className="pill">{s.operation}</span> : null}
                  {s.statusError ? <span className="pill border-red-700 bg-red-900/30 text-red-200">error</span> : null}
                </div>
                <div className="text-zinc-500 text-xs">
                  {fmtDuration(s.duration)} · in {fmtTokens(s.usage.inputTokens)} / out {fmtTokens(s.usage.outputTokens)}
                </div>
              </header>
              {s.messages?.length ? (
                <div className="mt-2 space-y-1">
                  {s.messages.map((m, i) => (
                    <div key={i} className="text-sm">
                      <span className="text-zinc-500 mr-2">{m.role}:</span>
                      <span className="whitespace-pre-wrap">{m.content}</span>
                    </div>
                  ))}
                </div>
              ) : null}
              {s.output ? (
                <div className="mt-2 text-sm whitespace-pre-wrap text-zinc-200">{s.output}</div>
              ) : null}
              {s.tool?.arguments ? (
                <pre className="mt-2 rounded bg-zinc-900 p-2 text-xs text-zinc-300 overflow-x-auto">{s.tool.arguments}</pre>
              ) : null}
              {s.tool?.result ? (
                <pre className="mt-2 rounded bg-zinc-900 p-2 text-xs text-zinc-300 overflow-x-auto">{s.tool.result}</pre>
              ) : null}
            </article>
          ))}
        </div>
      </section>

      {run.feedback?.length ? (
        <section>
          <h2 className="text-sm uppercase tracking-wide text-zinc-500 mb-2">Feedback</h2>
          <ul className="space-y-1 text-sm">
            {run.feedback.map((fb) => (
              <li key={fb.id} className="rounded border border-zinc-800 p-2">
                <span className="pill mr-2">{fb.source}</span>
                <span className="pill mr-2">score {fb.score.toFixed(2)}</span>
                {fb.label ? <span className="pill mr-2">{fb.label}</span> : null}
                {fb.comment ? <span>{fb.comment}</span> : null}
              </li>
            ))}
          </ul>
        </section>
      ) : null}
    </div>
  );
}
