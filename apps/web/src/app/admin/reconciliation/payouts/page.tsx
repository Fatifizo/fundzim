import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { ShortId } from "@/components/admin/operations/ops";
import { Facts, Section, StatusBadge, TableShell, When } from "@/components/admin/payments/common";
import { ExceptionTable, Money, ReconTabs, TestEvidenceBanner } from "@/components/admin/reconciliation/recon-view";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { readPayoutView } from "@/lib/reconciliation/admin";

export const metadata: Metadata = { title: "Payout reconciliation", robots: { index: false, follow: false, nocache: true } };

/**
 * Payouts in flight or with an UNKNOWN outcome, their attempt history and reservation, and payout-related exceptions.
 * Reconciliation never resubmits a payout and never releases or consumes a reservation.
 */
export default async function ReconciliationPayoutsPage() {
  await requireStaff();
  const v = readPayoutView((await serverGetOr404<unknown>("/api/v1/admin/reconciliation/payouts", "/admin/reconciliation/payouts")).data);
  return (
    <AccountPage title="Payout reconciliation" intro="TEST payouts through the simulator only.">
      <TestEvidenceBanner />
      <ReconTabs current="/admin/reconciliation/payouts" />
      <Section id="rules" title="What reconciliation may do with a payout">
        <ul className="list-disc space-y-1 pl-6 text-ink-700">
          {v.rules.map((r) => (
            <li key={r}>{r}</li>
          ))}
        </ul>
        <Facts items={[["Unknown outcome", String(v.unknown)], ["Automatic submission halted (retry budget spent)", String(v.halted)],
          ...v.matches.map((m): [string, string] => [`Matched lines — ledger ${humanise(m.key)}`, String(m.value)])]} />
      </Section>
      <Section id="queue" title="In flight or unknown">
        {v.queue.length === 0 ? (
          <p className="text-ink-700">No payout is in flight.</p>
        ) : (
          <TableShell caption="Payouts in flight or unknown" head={["Payout", "Status", "Amount", "Unknown since", "Reservation", "Submissions", "Refused before processing", "Last attempt", "Open exceptions"]} minWidthClass="min-w-[64rem]">
            {v.queue.map((p) => (
              <tr key={p.id} className="border-b border-line align-top last:border-0">
                <td className="p-3">
                  <ShortId value={p.id} />
                </td>
                <td className="p-3">
                  <StatusBadge status={p.status} />
                  {p.retryHalted ? <span className="block text-sm font-semibold text-ink-900">halted</span> : null}
                </td>
                <td className="p-3">
                  <Money minor={p.amount} currency={p.currency} />
                </td>
                <td className="p-3">{p.unknownSince ? <When at={p.unknownSince} /> : "—"}</td>
                <td className="p-3">{p.reservation ? humanise(p.reservation) : "—"}</td>
                <td className="p-3">{p.createAttempts}</td>
                <td className="p-3">{p.notSentAttempts}</td>
                <td className="p-3">{p.lastAttempt ? humanise(p.lastAttempt) : "—"}</td>
                <td className="p-3">{p.openExceptions.length > 0 ? p.openExceptions.map(humanise).join(", ") : "—"}</td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
      <Section id="exceptions" title="Payout exceptions from statements">
        <ExceptionTable items={v.exceptions} caption="Payout reconciliation exceptions" />
      </Section>
    </AccountPage>
  );
}
