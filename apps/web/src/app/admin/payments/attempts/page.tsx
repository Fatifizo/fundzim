import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { AttemptsTable, FilterForm, NextPage, PAYMENTS_BASE, toOptions } from "@/components/admin/payments/common";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readNextCursor } from "@/lib/campaigns/normalise";
import { isCursor } from "@/lib/campaigns/paths";
import { buildQuery, cursorParam, pick } from "@/lib/payments/labels";
import { readAttempts } from "@/lib/payments/normalise";
import { ATTEMPT_STATUSES, METHODS } from "@/lib/payments/types";

export const metadata: Metadata = { title: "Payment attempts", robots: { index: false, follow: false, nocache: true } };

const BASE = `${PAYMENTS_BASE}/attempts`;

/** Payments (`payment.view`), newest first. Filters: status, method, needs reconciliation. */
export default async function PaymentAttemptsPage({ searchParams }: PageProps<"/admin/payments/attempts">) {
  await requireStaff();
  const p = await searchParams;
  const status = pick(p.status, ATTEMPT_STATUSES);
  const method = pick(p.method, METHODS);
  const needs = pick(p.needs_reconciliation, ["true"] as const);
  const filters = { status, method, needs_reconciliation: needs };
  const result = await serverGetOr404<unknown>(`/api/v1/admin/payments/attempts${buildQuery({ ...filters, cursor: cursorParam(p.cursor) })}`, BASE);
  const items = readAttempts(result.data);
  const nextRaw = readNextCursor(result.data, result.meta);
  return (
    <AccountPage title="Payment attempts" intro="Payments, newest first. Open one for provider calls, the provider's raw status, webhooks and the event timeline.">
      <FilterForm
        action={BASE}
        label="Filter payments"
        fields={[
          { name: "status", label: "Status", value: status, options: toOptions(ATTEMPT_STATUSES) },
          { name: "method", label: "Method", value: method, options: toOptions(METHODS) },
          { name: "needs_reconciliation", label: "Reconciliation", value: needs, options: [{ value: "true", label: "Needs reconciliation" }] },
        ]}
      />
      <AttemptsTable items={items} caption="Payment attempts" />
      <NextPage base={BASE} query={filters} next={nextRaw && isCursor(nextRaw) ? nextRaw : null} />
    </AccountPage>
  );
}
