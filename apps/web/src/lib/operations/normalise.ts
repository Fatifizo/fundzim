import type { Metric, SourceStatus } from "./types";

/**
 * Defensive readers for operations responses: a missing list is empty, never a crash, and a missing number stays
 * null (shown as "Not measured"), never 0.
 */
const isObj = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);

export function obj(v: unknown): Record<string, unknown> {
  return isObj(v) ? v : {};
}

/** The `items` of a `{items, next_cursor}` page (or a bare array). */
export function items<T>(v: unknown): T[] {
  if (Array.isArray(v)) return v as T[];
  const list = obj(v).items;
  return Array.isArray(list) ? (list as T[]) : [];
}

export function list<T>(v: unknown, key: string): T[] {
  const x = obj(v)[key];
  return Array.isArray(x) ? (x as T[]) : [];
}

export function nextCursor(v: unknown): string | null {
  const c = obj(v).next_cursor;
  return typeof c === "string" && c !== "" ? c : null;
}

export function sources(v: unknown): SourceStatus[] {
  return list<SourceStatus>(v, "sources");
}

export function metrics(v: unknown): Metric[] {
  return list<Metric>(v, "metrics").map((m) => ({ ...m, value: typeof m.value === "number" ? m.value : null, status: m.status ?? null, href: m.href ?? null, state: typeof m.state === "string" && m.state !== "" ? m.state : null }));
}

/** "3 d 4 h", "2 h 5 min", "45 s" — an age or a target duration (whole units; no rounding up). */
export function formatDuration(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined || !Number.isFinite(seconds) || seconds < 0) return "—";
  const s = Math.floor(seconds);
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d > 0) return h > 0 ? `${d} d ${h} h` : `${d} d`;
  if (h > 0) return m > 0 ? `${h} h ${m} min` : `${h} h`;
  if (m > 0) return `${m} min`;
  return `${s} s`;
}
