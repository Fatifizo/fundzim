import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { inputClasses } from "@/components/forms/text-field";
import { OpsBanner, Section, StaffTable } from "@/components/admin/operations/ops";
import { buttonClasses } from "@/components/ui/button";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { buildQuery, pick } from "@/lib/payments/labels";
import { items } from "@/lib/operations/normalise";
import type { StaffMember } from "@/lib/operations/types";

export const metadata: Metadata = { title: "Staff directory", robots: { index: false, follow: false, nocache: true } };

const STATUSES = ["ACTIVE", "SUSPENDED", "CLOSED"] as const;

/**
 * Staff directory (`staff.view`): names, roles, MFA and session counts. No email address, phone number or password
 * data. Role changes are dual-controlled role requests; nobody grants themselves a role.
 */
export default async function StaffPage({ searchParams }: PageProps<"/admin/staff">) {
  await requireStaff();
  const p = await searchParams;
  const q = typeof p.q === "string" ? p.q.slice(0, 100) : undefined;
  const status = pick(p.status, STATUSES);
  const list = items<StaffMember>((await serverGetOr404<unknown>(`/api/v1/admin/staff${buildQuery({ q, status })}`, "/admin/staff")).data);
  return (
    <AccountPage title="Staff directory" intro="Staff accounts with their active roles. Opening a staff member is recorded in the security audit log.">
      <OpsBanner />
      <form method="get" action="/admin/staff" aria-label="Find staff" className="grid gap-4 rounded-card border border-line bg-surface p-4 sm:grid-cols-3 sm:items-end">
        <div className="space-y-1">
          <label htmlFor="staff-q" className="block font-semibold text-ink-900">
            Name or id
          </label>
          <input id="staff-q" name="q" defaultValue={q} maxLength={100} className={inputClasses} />
        </div>
        <div className="space-y-1">
          <label htmlFor="staff-status" className="block font-semibold text-ink-900">
            Status
          </label>
          <select id="staff-status" name="status" defaultValue={status ?? ""} className={inputClasses}>
            <option value="">Any</option>
            {STATUSES.map((s) => (
              <option key={s} value={s}>
                {s.charAt(0) + s.slice(1).toLowerCase()}
              </option>
            ))}
          </select>
        </div>
        <div>
          <button type="submit" className={buttonClasses("primary")}>
            Find
          </button>
        </div>
      </form>
      <Section id="list" title={`Staff (${list.length})`}>
        <StaffTable items={list} />
      </Section>
    </AccountPage>
  );
}
