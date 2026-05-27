import {
  createRootRoute,
  createRoute,
  Navigate,
} from "@tanstack/react-router";
import { Layout } from "./components/Layout";
import { TracesPage } from "./routes/traces";
import { TraceDetailPage } from "./routes/traceDetail";
import { TasksPage } from "./routes/tasks";
import { TaskDetailPage } from "./routes/taskDetail";
import { ComparePage } from "./routes/compare";
import { FeedbackPage } from "./routes/feedback";
import { FrequencyPage } from "./routes/frequency";
import { SettingsPage } from "./routes/settings";

const rootRoute = createRootRoute({ component: Layout });

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: () => <Navigate to="/traces" replace />,
});

const tracesRoute = createRoute({ getParentRoute: () => rootRoute, path: "/traces", component: TracesPage });
const traceDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/traces/$id",
  component: TraceDetailPage,
});

const tasksRoute = createRoute({ getParentRoute: () => rootRoute, path: "/tasks", component: TasksPage });
const taskDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/tasks/$id",
  component: TaskDetailPage,
});

const compareRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/compare",
  component: ComparePage,
  validateSearch: (s: Record<string, unknown>): { ids?: string } => ({
    ids: typeof s.ids === "string" ? s.ids : undefined,
  }),
});

const feedbackRoute = createRoute({ getParentRoute: () => rootRoute, path: "/feedback", component: FeedbackPage });
const frequencyRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/analytics/frequency",
  component: FrequencyPage,
});
const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/settings/sources",
  component: SettingsPage,
});

export const routeTree = rootRoute.addChildren([
  indexRoute,
  tracesRoute,
  traceDetailRoute,
  tasksRoute,
  taskDetailRoute,
  compareRoute,
  feedbackRoute,
  frequencyRoute,
  settingsRoute,
]);
