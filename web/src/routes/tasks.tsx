import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/api/client";
import { fmtTime, shortId } from "@/lib/format";

export function TasksPage() {
  const { data, isLoading, error } = useQuery({ queryKey: ["tasks"], queryFn: () => api.listTasks() });
  if (isLoading) return <div className="text-zinc-400">loading…</div>;
  if (error) return <div className="text-red-300">{String(error)}</div>;
  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold">Tasks</h1>
      <table className="table">
        <thead>
          <tr>
            <th>Task</th>
            <th>Source</th>
            <th>Runs</th>
            <th>Models</th>
            <th>Avg feedback</th>
            <th>Last seen</th>
          </tr>
        </thead>
        <tbody>
          {data?.tasks?.map((t) => (
            <tr key={t.id} className="hover:bg-zinc-900/50">
              <td>
                <Link to="/tasks/$id" params={{ id: t.id }} className="font-mono text-emerald-300">
                  {shortId(t.id, 28)}
                </Link>
              </td>
              <td>{t.source}</td>
              <td>{t.runCount}</td>
              <td>{t.models?.join(", ")}</td>
              <td>{t.avgScore != null ? t.avgScore.toFixed(2) : <span className="text-zinc-500">—</span>}</td>
              <td className="text-zinc-400 text-xs">{fmtTime(t.lastSeen)}</td>
            </tr>
          ))}
          {data?.tasks?.length === 0 ? (
            <tr>
              <td colSpan={6} className="py-6 text-center text-zinc-500">
                No tasks yet. Tasks appear once traces include gen_ai.task.id / .task.name /
                .conversation.id, or once you group runs manually.
              </td>
            </tr>
          ) : null}
        </tbody>
      </table>
    </div>
  );
}
