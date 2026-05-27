export function fmtTime(s: string | number | Date): string {
  const d = typeof s === "object" ? s : new Date(s);
  return d.toLocaleString();
}

export function fmtDuration(nsOrMs: number, unit: "ns" | "ms" = "ns"): string {
  let ms = unit === "ns" ? nsOrMs / 1e6 : nsOrMs;
  if (ms < 1) return `${(ms * 1000).toFixed(1)}µs`;
  if (ms < 1000) return `${ms.toFixed(1)}ms`;
  return `${(ms / 1000).toFixed(2)}s`;
}

export function fmtTokens(n: number | undefined): string {
  if (!n) return "0";
  if (n < 1000) return String(n);
  return `${(n / 1000).toFixed(1)}k`;
}

export function shortId(id: string, head = 8): string {
  if (!id) return "";
  if (id.length <= head) return id;
  return id.slice(0, head) + "…";
}
