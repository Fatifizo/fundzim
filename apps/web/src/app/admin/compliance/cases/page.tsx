import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { formatDateTime } from "@/components/account/format";
import { inputClasses } from "@/components/forms/text-field";
import { Badge } from "@/components/ui/badge";
import { ButtonLink, buttonClasses } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { complianceStatusLabel, humanise } from "@/lib/verification/labels";
import { normaliseComplianceList } from "@/lib/verification/normalise";
import { COMPLIANCE_STATUSES } from "@/lib/verification/types";

export const metadata: Metadata = { title: "Compliance cases", robots: { index: false, follow: false, nocache: true } };

/** Severity scale of the compliance module (S1 most severe). */
const SEVERITIES = ["S1", "S2", "S3"] as const;
const SEVERITY_LABELS: Record<string, string> = { S1: "S1 (highest)", S2: "S2", S3: "S3 (lowest)" };
const BASE = "/admin/compliance/cases";

function first(value: string | string[] | undefined): string | undefined {
  return Array.isArray(value) ? value[0] : value;
}

export default async function ComplianceCasesPage({ searchParams }: PageProps<"/admin/compliance/cases">) {
  const me = await requireStaff();
  const params = await searchParams;
  const status = (COMPLIANCE_STATUSES as readonly string[]).includes(first(params.status) ?? "") ? first(params.status) : undefined;
  const severity = (SEVERITIES as readonly string[]).includes(first(params.severity) ?? "") ? first(params.severity) : undefined;
  const assignedRaw = first(params.assigned);
  const assigned = assignedRaw === "me" || assignedRaw === "unassigned" ? assignedRaw : "any";
  const cursorRaw = first(params.cursor);
  const cursor = cursorRaw && /^[\x21-\x7e]{1,512}$/.test(cursorRaw) ? cursorRaw : undefined;
  const q = new URLSearchParams();
  if (status) q.set("status", status);
  if (severity) q.set("severity", severity);
  q.set("assigned", assigned);
  q.set("limit", "25");
  if (cursor) q.set("cursor", cursor);
  const result = await serverGetOr404<unknown>(`/api/v1/admin/compliance/cases?${q.toString()}`, BASE);
  const { items, nextCursor } = normaliseComplianceList(result.data, result.meta.next_cursor, me.id);
  const next = nextCursor
    ? `${BASE}?${new URLSearchParams({ ...(status ? { status } : {}), ...(severity ? { severity } : {}), ...(assigned !== "any" ? { assigned } : {}), cursor: nextCursor }).toString()}`
    : null;

  return (
    <AccountPage title="Compliance cases" intro="Cases opened from escalated verifications, risk recommendations or manually. Notes stay inside compliance.">
      <form method="get" action={BASE} className="grid gap-4 rounded-card border border-line bg-surface p-4 sm:grid-cols-4 sm:items-end" aria-label="Filter compliance cases">
        <div className="space-y-1">
          <label htmlFor="c-status" className="block font-semibold text-ink-900">Status</label>
          <select id="c-status" name="status" defaultValue={status ?? ""} className={inputClasses}>
            <option value="">Any status</option>
            {COMPLIANCE_STATUSES.map((s) => (
              <option key={s} value={s}>{complianceStatusLabel(s).label}</option>
            ))}
          </select>
        </div>
        <div className="space-y-1">
          <label htmlFor="c-severity" className="block font-semibold text-ink-900">Severity</label>
          <select id="c-severity" name="severity" defaultValue={severity ?? ""} className={inputClasses}>
            <option value="">Any severity</option>
            {SEVERITIES.map((s) => (
              <option key={s} value={s}>{SEVERITY_LABELS[s]}</option>
            ))}
          </select>
        </div>
        <div className="space-y-1">
          <label htmlFor="c-assigned" className="block font-semibold text-ink-900">Assigned</label>
          <select id="c-assigned" name="assigned" defaultValue={assigned} className={inputClasses}>
            <option value="any">Anyone or no one</option>
            <option value="me">Assigned to me</option>
            <option value="unassigned">Unassigned</option>
          </select>
        </div>
        <div>
          <button type="submit" className={buttonClasses("primary")}>Apply filters</button>
        </div>
      </form>
      {items.length === 0 ? (
        <EmptyState title="No cases match" description="Change the filters or check again later." />
      ) : (
        <div className="overflow-x-auto rounded-card border border-line bg-surface">
          <table className="w-full min-w-[44rem] text-left text-ink-700">
            <caption className="sr-only">Compliance cases</caption>
            <thead>
              <tr className="border-b border-line text-sm text-ink-600">
                <th scope="col" className="p-3">Case</th>
                <th scope="col" className="p-3">Subject</th>
                <th scope="col" className="p-3">Type</th>
                <th scope="col" className="p-3">Status</th>
                <th scope="col" className="p-3">Severity</th>
                <th scope="col" className="p-3">Assigned to</th>
                <th scope="col" className="p-3">Opened</th>
              </tr>
            </thead>
            <tbody>
              {items.map((c) => {
                const label = complianceStatusLabel(c.status);
                return (
                  <tr key={c.id} className="border-b border-line align-top last:border-b-0">
                    <th scope="row" className="p-3 font-semibold">
                      <Link href={`${BASE}/${encodeURIComponent(c.id)}`} className="text-brand-700 underline break-words">
                        {c.reference ?? c.id.slice(0, 8)}
                      </Link>
                    </th>
                    <td className="p-3 break-words">{c.subject ? c.subject.display_name ?? humanise(c.subject.type) : "—"}</td>
                    <td className="p-3">{humanise(c.case_type)}</td>
                    <td className="p-3"><Badge tone={label.tone}>{label.label}</Badge></td>
                    <td className="p-3">{SEVERITY_LABELS[c.severity] ?? humanise(c.severity)}</td>
                    <td className="p-3">{c.assigned_to?.display_name ?? "Unassigned"}</td>
                    <td className="p-3">{formatDateTime(c.opened_at)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      {next ? <ButtonLink href={next} variant="outline">Next page</ButtonLink> : null}
    </AccountPage>
  );
}
