import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { AttemptsTable, NextPage, PAYMENTS_BASE } from "@/components/admin/payments/common";
import { Alert } from "@/components/ui/alert";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readNextCursor } from "@/lib/campaigns/normalise";
import { isCursor } from "@/lib/campaigns/paths";
import { buildQuery, cursorParam } from "@/lib/payments/labels";
import { readAttempts } from "@/lib/payments/normalise";

export const metadata: Metadata = { title: "Unknown payments", robots: { index: false, follow: false, nocache: true } };

const BASE = `${PAYMENTS_BASE}/unknown`;

/** The UNKNOWN queue (`payment.view`), oldest first. */
export default async function UnknownPaymentsPage({ searchParams }: PageProps<"/admin/payments/unknown">) {
  await requireStaff();
  const p = await searchParams;
  const result = await serverGetOr404<unknown>(`/api/v1/admin/payments/unknown${buildQuery({ cursor: cursorParam(p.cursor) })}`, BASE);
  const items = readAttempts(result.data);
  const nextRaw = readNextCursor(result.data, result.meta);
  return (
    <AccountPage title="Unknown payments" intro="Payments whose outcome is not yet known, oldest first.">
      <Alert tone="info" title="Unknown is not failed">
        An unknown outcome is resolved only by an authoritative provider status query, a verified webhook or reconciliation — never by assuming
        failure. Open a payment to request a status query.
      </Alert>
      <AttemptsTable items={items} caption="Payments with an unknown outcome" empty="No payments with an unknown outcome" />
      <NextPage base={BASE} query={{}} next={nextRaw && isCursor(nextRaw) ? nextRaw : null} />
    </AccountPage>
  );
}
