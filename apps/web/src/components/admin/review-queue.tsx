import Link from "next/link";

import { formatDateTime } from "@/components/account/format";
import { inputClasses } from "@/components/forms/text-field";
import { CaseStatusBadge } from "@/components/verification/status";
import { Badge } from "@/components/ui/badge";
import { ButtonLink, buttonClasses } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { humanise } from "@/lib/verification/labels";
import { CASE_STATUSES, type ReviewCaseSummary, type ReviewCaseType } from "@/lib/verification/types";

export const QUEUE_TYPES: ReadonlyArray<{ value: ReviewCaseType; label: string; path: string }> = [
  { value: "KYC", label: "Identity (KYC)", path: "/admin/verification/kyc" },
  { value: "KYB", label: "Organisation (KYB)", path: "/admin/verification/kyb" },
  { value: "BENEFICIARY", label: "Beneficiary", path: "/admin/verification/beneficiaries" },
  { value: "PAYOUT_DESTINATION", label: "Payout account", path: "/admin/verification/payout-destinations" },
];

const QUEUE_STATUSES = [...CASE_STATUSES, "PENDING_VERIFICATION", "AWAITING_SECOND_APPROVAL"] as const;
const ASSIGNED = [
  { value: "any", label: "Anyone or no one" },
  { value: "me", label: "Assigned to me" },
  { value: "unassigned", label: "Unassigned" },
] as const;

export interface QueueFilters {
  type?: ReviewCaseType;
  status?: string;
  assigned: "me" | "unassigned" | "any";
  cursor?: string;
}

function first(value: string | string[] | undefined): string | undefined {
  return Array.isArray(value) ? value[0] : value;
}

/** Allow-lists query parameters before they are forwarded to the API (unknown values are dropped). */
export function parseQueueFilters(params: Record<string, string | string[] | undefined>, fixedType?: ReviewCaseType): QueueFilters {
  const type = fixedType ?? (QUEUE_TYPES.find((t) => t.value === first(params.type))?.value);
  const statusRaw = first(params.status);
  const status = statusRaw && (QUEUE_STATUSES as readonly string[]).includes(statusRaw) ? statusRaw : undefined;
  const assignedRaw = first(params.assigned);
  const assigned = assignedRaw === "me" || assignedRaw === "unassigned" ? assignedRaw : "any";
  const cursorRaw = first(params.cursor);
  const cursor = cursorRaw && /^[\x21-\x7e]{1,512}$/.test(cursorRaw) ? cursorRaw : undefined;
  return { type, status, assigned, cursor };
}

export function queueApiPath(filters: QueueFilters, limit = 25): string {
  const q = new URLSearchParams();
  if (filters.type) q.set("type", filters.type);
  if (filters.status) q.set("status", filters.status);
  q.set("assigned", filters.assigned);
  q.set("limit", String(limit));
  if (filters.cursor) q.set("cursor", filters.cursor);
  return `/api/v1/admin/verification/cases?${q.toString()}`;
}

function typeLabel(type: string): string {
  return QUEUE_TYPES.find((t) => t.value === type)?.label ?? humanise(type);
}

export function ReviewQueue({
  basePath,
  filters,
  fixedType,
  cases,
  nextCursor,
}: {
  basePath: string;
  filters: QueueFilters;
  fixedType?: ReviewCaseType;
  cases: ReviewCaseSummary[];
  nextCursor?: string;
}) {
  const nextHref = (() => {
    if (!nextCursor) return null;
    const q = new URLSearchParams();
    if (!fixedType && filters.type) q.set("type", filters.type);
    if (filters.status) q.set("status", filters.status);
    if (filters.assigned !== "any") q.set("assigned", filters.assigned);
    q.set("cursor", nextCursor);
    return `${basePath}?${q.toString()}`;
  })();
  return (
    <div className="space-y-6">
      <form method="get" action={basePath} className="grid gap-4 rounded-card border border-line bg-surface p-4 sm:grid-cols-4 sm:items-end" aria-label="Filter the queue">
        {!fixedType ? (
          <div className="space-y-1">
            <label htmlFor="filter-type" className="block font-semibold text-ink-900">Type</label>
            <select id="filter-type" name="type" defaultValue={filters.type ?? ""} className={inputClasses}>
              <option value="">All types</option>
              {QUEUE_TYPES.map((t) => (
                <option key={t.value} value={t.value}>{t.label}</option>
              ))}
            </select>
          </div>
        ) : null}
        <div className="space-y-1">
          <label htmlFor="filter-status" className="block font-semibold text-ink-900">Status</label>
          <select id="filter-status" name="status" defaultValue={filters.status ?? ""} className={inputClasses}>
            <option value="">Any status</option>
            {QUEUE_STATUSES.map((s) => (
              <option key={s} value={s}>{humanise(s)}</option>
            ))}
          </select>
        </div>
        <div className="space-y-1">
          <label htmlFor="filter-assigned" className="block font-semibold text-ink-900">Assigned</label>
          <select id="filter-assigned" name="assigned" defaultValue={filters.assigned} className={inputClasses}>
            {ASSIGNED.map((a) => (
              <option key={a.value} value={a.value}>{a.label}</option>
            ))}
          </select>
        </div>
        <div>
          <button type="submit" className={buttonClasses("primary")}>Apply filters</button>
        </div>
      </form>

      {cases.length === 0 ? (
        <EmptyState title="No cases match" description="Change the filters or check again later." />
      ) : (
        <div className="overflow-x-auto rounded-card border border-line bg-surface">
          <table className="w-full min-w-[44rem] text-left text-ink-700">
            <caption className="sr-only">Verification cases</caption>
            <thead>
              <tr className="border-b border-line text-sm text-ink-600">
                <th scope="col" className="p-3">Subject</th>
                <th scope="col" className="p-3">Type</th>
                <th scope="col" className="p-3">Status</th>
                <th scope="col" className="p-3">Risk</th>
                <th scope="col" className="p-3">Assigned to</th>
                <th scope="col" className="p-3">Submitted</th>
              </tr>
            </thead>
            <tbody>
              {cases.map((c) => (
                <tr key={c.id} className="border-b border-line align-top last:border-b-0">
                  <th scope="row" className="p-3 font-semibold">
                    <Link href={`/admin/verification/cases/${encodeURIComponent(c.id)}`} className="text-brand-700 underline break-words">
                      {c.subject.display_name || `${humanise(c.subject.type)} ${c.subject.id.slice(0, 8)}`}
                    </Link>
                  </th>
                  <td className="p-3">{typeLabel(c.type)}</td>
                  <td className="p-3"><CaseStatusBadge status={c.status} audience="reviewer" /></td>
                  <td className="p-3">{c.risk_level ? <Badge tone={c.risk_level === "ENHANCED" || c.risk_level === "RESTRICTED" ? "gold" : "neutral"}>{humanise(c.risk_level)}</Badge> : "—"}</td>
                  <td className="p-3">{c.assigned_to?.display_name ?? "Unassigned"}</td>
                  <td className="p-3">{formatDateTime(c.submitted_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {nextHref ? (
        <ButtonLink href={nextHref} variant="outline">
          Next page
        </ButtonLink>
      ) : null}
    </div>
  );
}
