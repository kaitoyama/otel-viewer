// Thin fetch wrapper. The bearer token, when present, is read from local
// storage so the user can paste it via /settings/sources without bundling
// secrets at build time.
export type Filter = {
  model?: { in?: string[]; eq?: string };
  task_id?: { in?: string[]; eq?: string };
  vendor?: { in?: string[]; eq?: string };
  ingest_source?: { in?: string[]; eq?: string };
  replay_group?: string;
  feedback_score?: { lte?: number; lt?: number; gte?: number };
  tool_name?: { in?: string[]; eq?: string };
  tool_output_contains?: string;
  time?: { gte?: number; lte?: number };
};

const TOKEN_KEY = "otel-viewer.token";

export function getToken(): string {
  try {
    return localStorage.getItem(TOKEN_KEY) ?? "";
  } catch {
    return "";
  }
}
export function setToken(v: string) {
  try {
    localStorage.setItem(TOKEN_KEY, v);
  } catch {
    /* ignore */
  }
}

async function call<T>(path: string, init: RequestInit = {}): Promise<T> {
  const token = getToken();
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (token) headers.Authorization = `Bearer ${token}`;
  if (init.headers) Object.assign(headers, init.headers);
  const res = await fetch(path, { ...init, headers });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`${res.status}: ${text || res.statusText}`);
  }
  return res.json() as Promise<T>;
}

export type Usage = { inputTokens: number; outputTokens: number; totalTokens: number; costUsd?: number };

export type RunListItem = {
  traceId: string;
  name: string;
  startTime: string;
  endTime: string;
  model: string;
  vendor: string;
  taskId: string;
  ingestSource: string;
  tags: Record<string, string>;
  totals: Usage;
  stepCount: number;
  hasFeedback: boolean;
  minFeedback?: number;
  replayGroup?: string;
  replayOf?: string;
};

export type AgentStep = {
  index: number;
  spanId: string;
  parentSpanId?: string;
  kind: "PlannerLLM" | "ToolCall" | "Observation" | "AgentInvoke" | "Other";
  startTime: string;
  duration: number;
  model?: string;
  operation?: string;
  agentName?: string;
  tool?: { name: string; arguments?: string; result?: string; error?: string };
  messages?: { role: string; content?: string; name?: string }[];
  output?: string;
  usage: Usage;
  statusError?: string;
};

export type Run = RunListItem & {
  id: string;
  steps: AgentStep[];
  feedback?: { id: string; source: string; score: number; label?: string; comment?: string; createdAt: string }[];
  promptHash: string;
  taskSource: string;
};

export const api = {
  listTraces(filter?: Filter, limit = 100): Promise<{ runs: RunListItem[] }> {
    const qs = new URLSearchParams({ limit: String(limit) });
    if (filter) qs.set("filter", JSON.stringify(filter));
    return call(`/api/traces?${qs}`);
  },
  getTrace(id: string): Promise<Run> {
    return call(`/api/traces/${id}`);
  },
  listTasks(): Promise<{ tasks: { id: string; runCount: number; models: string[]; lastSeen: string; avgScore?: number; source: string }[] }> {
    return call(`/api/tasks`);
  },
  getTask(id: string): Promise<{ task: { id: string; models: string[]; runCount: number; promptHashes?: string[] }; runs: RunListItem[] }> {
    return call(`/api/tasks/${encodeURIComponent(id)}`);
  },
  addTaskMember(taskId: string, traceId: string) {
    return call(`/api/tasks/${encodeURIComponent(taskId)}/members`, {
      method: "POST",
      body: JSON.stringify({ traceId }),
    });
  },
  removeTaskMember(taskId: string, traceId: string) {
    return call(`/api/tasks/${encodeURIComponent(taskId)}/members/${traceId}`, { method: "DELETE" });
  },
  compare(traceIds: string[]): Promise<{
    runs: Run[];
    alignment: { indices: number[] }[];
    summary: { latencyMs: number[]; totalTokens: number[]; stepCount: number[]; avgFeedbackScore: number[] };
  }> {
    return call(`/api/compare`, { method: "POST", body: JSON.stringify({ traceIds }) });
  },
  postFeedback(body: { traceId: string; spanId?: string; source: string; score: number; label?: string; comment?: string }) {
    return call(`/api/feedback`, { method: "POST", body: JSON.stringify(body) });
  },
  listFeedback(traceId?: string, maxScore?: number) {
    const qs = new URLSearchParams();
    if (traceId) qs.set("traceId", traceId);
    if (maxScore != null) qs.set("maxScore", String(maxScore));
    return call<{ feedback: { id: string; traceId: string; score: number; label?: string; comment?: string; createdAt: string }[] }>(
      `/api/feedback?${qs}`,
    );
  },
  frequency(filter?: Filter) {
    const qs = new URLSearchParams();
    if (filter) qs.set("filter", JSON.stringify(filter));
    return call<{ rows: FrequencyRow[] }>(`/api/analytics/frequency?${qs}`);
  },
  flow(filter?: Filter) {
    const qs = new URLSearchParams();
    if (filter) qs.set("filter", JSON.stringify(filter));
    return call<{ nodes: FlowNode[]; edges: FlowEdge[] }>(`/api/analytics/flow?${qs}`);
  },
  listBackfills() {
    return call<BackfillJob[]>(`/api/datasources/tempo/backfill`);
  },
  startBackfill(body: {
    baseUrl: string;
    traceql?: string;
    startNs: number;
    endNs: number;
    limit?: number;
    authMode?: string;
    token?: string;
    username?: string;
    password?: string;
    tenantHeader?: string;
  }) {
    return call<{ jobId: string }>(`/api/datasources/tempo/backfill`, {
      method: "POST",
      body: JSON.stringify(body),
    });
  },
};

export type FrequencyRow = {
  model: string;
  agentName: string;
  toolName: string;
  operation: string;
  calls: number;
  errors: number;
  errorRate: number;
  avgDurationMs: number;
  p95DurationMs: number;
  avgInputTokens: number;
  avgOutputTokens: number;
};

export type FlowNode = {
  id: string;
  kind: string;
  agentName?: string;
  toolName?: string;
  operation?: string;
  count: number;
};
export type FlowEdge = { from: string; to: string; count: number };

export type BackfillJob = {
  id: string;
  baseUrl: string;
  traceql: string;
  startNs: number;
  endNs: number;
  status: string;
  fetched: number;
  total: number;
  startedAt: string;
  finishedAt?: string;
  error?: string;
};
