import { Link, Outlet, useRouterState } from "@tanstack/react-router";
import { clsx } from "clsx";

type NavItem = { to: string; label: string };
type NavGroup = { label: string; items: NavItem[] };

const groups: NavGroup[] = [
  {
    label: "Explore",
    items: [
      { to: "/traces", label: "Traces" },
      { to: "/tasks", label: "Tasks" },
    ],
  },
  {
    label: "Analyze",
    items: [
      { to: "/analytics/frequency", label: "Frequency" },
      { to: "/feedback", label: "Feedback" },
    ],
  },
  {
    label: "Settings",
    items: [{ to: "/settings/sources", label: "Sources" }],
  },
];

export function Layout() {
  const { location } = useRouterState();
  return (
    <div className="grid min-h-screen grid-cols-[14rem_1fr]">
      <aside className="border-r border-zinc-800 bg-zinc-950 p-4">
        <Link to="/traces" className="block text-lg font-semibold mb-6">
          otel-viewer
        </Link>
        {groups.map((g) => (
          <div key={g.label} className="mb-5">
            <div className="text-xs uppercase tracking-wide text-zinc-500 mb-1">{g.label}</div>
            {g.items.map((it) => (
              <Link
                key={it.to}
                to={it.to}
                className={clsx(
                  "block rounded px-2 py-1 text-sm hover:bg-zinc-800/70",
                  location.pathname.startsWith(it.to) && "bg-zinc-800/70 text-emerald-300",
                )}
              >
                {it.label}
              </Link>
            ))}
          </div>
        ))}
      </aside>
      <main className="overflow-x-hidden p-6">
        <Outlet />
      </main>
    </div>
  );
}
