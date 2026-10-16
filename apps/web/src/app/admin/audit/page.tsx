import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { inputClasses } from "@/components/forms/text-field";
import { NextPage } from "@/components/admin/payments/common";
import { AuditTable, NoPermission, OpsBanner, Section } from "@/components/admin/operations/ops";
import { AuditExportForm } from "@/components/admin/operations/ops-forms";
import { buttonClasses } from "@/components/ui/button";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { buildQuery } from "@/lib/payments/labels";
import { can } from "@/lib/operations/navigation";
import { items, nextCursor } from "@/lib/operations/normalise";
import type { AuditRecord } from "@/lib/operations/types";

export const metadata: Metadata = { title: "Audit log", robots: { index: false, follow: false, nocache: true } };

const BASE = "/admin/audit";
const FIELDS = [
  ["action", "Action prefix", "e.g. risk.rule."],
  ["actor_id", "Actor id", ""],
  ["target_type", "Target type", ""],
  ["target_id", "Target id", ""],
  ["correlation_id", "Correlation id", ""],
  ["request_id", "Request id", ""],
] as const;

/**
 * Audit center (`audit.read`; the security stream needs `security_audit.read`, exports `audit.export`). Records are
 * append-only and hash-chained: nothing here edits them. Network details are never shown in the list.
 */
export default async function AuditPage({ searchParams }: PageProps<"/admin/audit">) {
  const me = await requireStaff();
  const p = await searchParams;
  const one = (v: string | string[] | undefined) => (typeof v === "string" && v.length <= 200 ? v : undefined);
  const security = p.stream === "security" && can(me.permissions, "security_audit.read");
  const filters: Record<string, string | undefined> = {};
  for (const [k] of FIELDS) filters[k] = one(p[k]);
  const cursor = one(p.cursor);
  const path = security ? "/api/v1/admin/audit/security-events" : "/api/v1/admin/audit/events";
  const data = (await serverGetOr404<unknown>(`${path}${buildQuery({ ...filters, cursor, limit: "50" })}`, BASE)).data;
  return (
    <AccountPage title="Audit log" intro="Append-only, hash-chained records of staff and system actions. Searching the audit log is itself audited.">
      <OpsBanner />
      {can(me.permissions, "security_audit.read") ? (
        <nav aria-label="Audit streams" className="flex gap-3">
          <Link href={BASE} aria-current={!security ? "page" : undefined} className="underline underline-offset-4 aria-[current=page]:font-semibold">
            Application audit
          </Link>
          <Link href={`${BASE}?stream=security`} aria-current={security ? "page" : undefined} className="underline underline-offset-4 aria-[current=page]:font-semibold">
            Security audit
          </Link>
        </nav>
      ) : null}
      <form method="get" action={BASE} aria-label="Search audit records" className="grid gap-4 rounded-card border border-line bg-surface p-4 sm:grid-cols-3 sm:items-end">
        {security ? <input type="hidden" name="stream" value="security" /> : null}
        {FIELDS.map(([k, label, ph]) => (
          <div key={k} className="space-y-1">
            <label htmlFor={`audit-${k}`} className="block font-semibold text-ink-900">
              {label}
            </label>
            <input id={`audit-${k}`} name={k} defaultValue={filters[k]} placeholder={ph || undefined} maxLength={200} autoComplete="off" className={inputClasses} />
          </div>
        ))}
        <div>
          <button type="submit" className={buttonClasses("primary")}>
            Search
          </button>
        </div>
      </form>
      <Section id="records" title={security ? "Security audit records" : "Audit records"}>
        <AuditTable items={items<AuditRecord>(data)} caption={security ? "Security audit records" : "Audit records"} />
        <NextPage base={BASE} query={{ ...filters, stream: security ? "security" : undefined }} next={nextCursor(data)} label="Older records" />
      </Section>
      <Section id="export" title="Export" description="At most 31 days and 5,000 records per export. Spreadsheet formulas are neutralised. The export is recorded in the security audit log.">
        {can(me.permissions, "audit.export") ? <AuditExportForm /> : <NoPermission action="Exporting audit records" permission="audit.export" />}
      </Section>
    </AccountPage>
  );
}
