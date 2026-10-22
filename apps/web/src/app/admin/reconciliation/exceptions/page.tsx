import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FilterForm, Section, toOptions } from "@/components/admin/payments/common";
import { ExceptionTable, ReconTabs, TestEvidenceBanner } from "@/components/admin/reconciliation/recon-view";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readExceptions } from "@/lib/reconciliation/admin";

export const metadata: Metadata = { title: "Reconciliation exceptions", robots: { index: false, follow: false, nocache: true } };

const STATUSES = ["OPEN", "ASSIGNED", "INVESTIGATING", "AWAITING_EVIDENCE", "PENDING_APPROVAL", "RESOLVED", "REOPENED", "CLOSED"] as const;
const SEVERITIES = ["CRITICAL", "HIGH", "MEDIUM", "LOW"] as const;
const TYPES = ["MISSING_PAYMENT", "MISSING_SETTLEMENT", "MISSING_PAYOUT", "MISSING_LEDGER_ENTRY", "MISSING_BANK_CREDIT", "AMOUNT_MISMATCH",
  "CURRENCY_MISMATCH", "FEE_MISMATCH", "PAYOUT_MISMATCH", "STATUS_MISMATCH", "DUPLICATE_TRANSACTION", "AMBIGUOUS_MATCH", "UNKNOWN_PAYMENT_OUTCOME",
  "UNKNOWN_PAYOUT_OUTCOME", "UNMATCHED_BANK_ENTRY", "POSSIBLE_DONOR_FUNDS_IN_OPERATING_ACCOUNT", "SETTLEMENT_SHORT", "SETTLEMENT_OVER",
  "CONFLICTING_EVIDENCE", "PROVIDER_ADJUSTMENT", "STALE_RECONCILIATION"] as const;

const pick = (v: string | string[] | undefined, allowed: readonly string[]) => (typeof v === "string" && allowed.includes(v) ? v : undefined);

/** The exception queue: most severe, then oldest first. */
export default async function ReconciliationExceptionsPage({ searchParams }: PageProps<"/admin/reconciliation/exceptions">) {
  await requireStaff();
  const sp = await searchParams;
  const status = pick(sp.status, STATUSES);
  const severity = pick(sp.severity, SEVERITIES);
  const type = pick(sp.type, TYPES);
  const open = sp.open === "true" && !status;
  const q = new URLSearchParams({ limit: "200" });
  if (status) q.set("status", status);
  if (severity) q.set("severity", severity);
  if (type) q.set("type", type);
  if (open) q.set("open", "true");
  const items = readExceptions((await serverGetOr404<unknown>(`/api/v1/admin/reconciliation/exceptions?${q}`, "/admin/reconciliation/exceptions")).data);
  return (
    <AccountPage title="Reconciliation exceptions" intro="Every difference between FundZim's records, statements and the ledger. Severities are engineering defaults, not regulatory classifications.">
      <TestEvidenceBanner />
      <ReconTabs current="/admin/reconciliation/exceptions" />
      <FilterForm action="/admin/reconciliation/exceptions" label="Filter exceptions" fields={[
        { name: "status", label: "Status", value: status, options: toOptions(STATUSES) },
        { name: "severity", label: "Severity", value: severity, options: toOptions(SEVERITIES) },
        { name: "type", label: "Type", value: type, options: toOptions(TYPES) },
      ]} />
      <Section id="queue" title={open ? "Open exceptions" : "Exceptions"}>
        <ExceptionTable items={items} caption="Reconciliation exceptions" />
      </Section>
    </AccountPage>
  );
}
