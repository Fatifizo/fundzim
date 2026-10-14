import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { FINANCE_BASE, LedgerAmount } from "@/components/admin/finance/common";
import { PayoutAction } from "@/components/admin/finance/payout-action";
import { ChecksTable, PayoutBanner, WITHDRAWALS_BASE, withdrawalHref } from "@/components/admin/finance/payouts";
import { Code, Facts, Section, StatusBadge, TableShell, When } from "@/components/admin/payments/common";
import { Badge } from "@/components/ui/badge";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { isUuid } from "@/lib/campaigns/paths";
import { humanise } from "@/lib/payments/labels";
import { readDetail } from "@/lib/payouts/normalise";
import type { Row } from "@/lib/payouts/types";

export const metadata: Metadata = { title: "Withdrawal request", robots: { index: false, follow: false, nocache: true } };

const text = (v: Row[string] | undefined) => (v === null || v === undefined || v === "" ? "—" : String(v));

/** One withdrawal (payout) request: everything recorded about it, and the staff actions its state allows. */
export default async function WithdrawalPage({ params }: PageProps<"/admin/finance/withdrawals/[id]">) {
  await requireStaff();
  const { id } = await params;
  if (!isUuid(id)) notFound();
  const result = await serverGetOr404<unknown>(`/api/v1/admin/payout-requests/${id}`, withdrawalHref(id));
  const d = readDetail(result.data);
  if (!d) notFound();
  const p = d.payout;
  const r = p.reservation;
  const journalHref = (tx: string) => `${FINANCE_BASE}/journals/${encodeURIComponent(tx)}`;
  return (
    <AccountPage title="Withdrawal request" intro={<Link href={WITHDRAWALS_BASE}>All withdrawal requests</Link>}>
      <PayoutBanner />
      <Section id="payout" title="Payout">
        <p className="flex flex-wrap items-center gap-2">
          <StatusBadge status={p.status} /> {p.test_payout ? <Badge tone="gold">TEST_ONLY</Badge> : null}
          {p.needs_reconciliation ? <Badge tone="gold">Needs reconciliation</Badge> : null}
        </p>
        <Facts
          items={[
            ["Amount", <LedgerAmount key="a" minor={p.amount_minor} currency={p.currency} />],
            ["Campaign", <Code key="c">{p.campaign_id}</Code>],
            ["Requested by", <Code key="r">{p.requested_by}</Code>],
            ["Requested", <When key="w" at={p.requested_at} />],
            ["Purpose", p.purpose ?? "—"],
            ["Approval", `${humanise(p.approval_policy)} · round ${p.approval_round}`],
            ["Destination", `${humanise(p.destination_rail)} ${p.destination_masked} (${humanise(p.destination_category)})`],
            ["Execution environment", p.execution_environment],
            ["Ledger environment", p.ledger_environment],
            ["Provider reference", p.provider_payout_ref ? <Code key="pr">{p.provider_payout_ref}</Code> : "—"],
            ["Unknown since", p.unknown_since ? `${p.unknown_since} (${p.unknown_reason ?? "—"})` : "—"],
            ["Failure / decision", p.failure_code ?? p.decision_reason_code ?? "—"],
          ]}
        />
      </Section>
      <Section id="actions" title="Actions">
        <div className="flex flex-wrap gap-4">
          {p.status === "PENDING_REVIEW" ? (
            <>
              <PayoutAction target={{ kind: "approve", id: p.id }} />
              <PayoutAction target={{ kind: "reject", id: p.id }} />
              <PayoutAction target={{ kind: "recheck", id: p.id }} />
            </>
          ) : null}
          {p.status === "PENDING_REVIEW" || p.status === "APPROVED" ? <PayoutAction target={{ kind: "cancel", id: p.id }} /> : null}
          {["SUBMITTED", "PROCESSING", "UNKNOWN", "COMPLETED"].includes(p.status) ? <PayoutAction target={{ kind: "status", id: p.id }} /> : null}
        </div>
      </Section>
      <Section id="reservation" title="Reservation">
        {r ? (
          <Facts
            items={[
              ["Status", <StatusBadge key="s" status={r.status} />],
              ["Amount", <LedgerAmount key="a" minor={r.amount_minor} currency={r.currency} />],
              ["Ledger account", <Code key="ac">{r.account_scope}</Code>],
              ["Expires", r.expires_at ? <When key="e" at={r.expires_at} /> : "No validity limit configured"],
            ]}
          />
        ) : (
          <p className="text-ink-700">No reservation yet (the request is being reserved, or it was refused before reserving).</p>
        )}
      </Section>
      <Section id="journals" title="Ledger journals">
        <TableShell caption="Payout ledger journals" head={["Step", "Journal"]} minWidthClass="min-w-[28rem]">
          {d.journals.map((j) => (
            <tr key={j.idempotency_key} className="border-b border-line last:border-b-0">
              <th scope="row" className="p-3 font-normal">
                {humanise(j.idempotency_key.split(":").pop() ?? "")}
              </th>
              <td className="p-3">
                {j.transaction_id ? (
                  <Link href={journalHref(j.transaction_id)} className="text-brand-700 underline">
                    Open journal
                  </Link>
                ) : (
                  "—"
                )}
              </td>
            </tr>
          ))}
        </TableShell>
      </Section>
      <Section id="decisions" title="Eligibility decisions">
        {d.decisions.map((dec) => (
          <div key={dec.id} className="space-y-2">
            <p className="font-semibold text-ink-900">
              {humanise(dec.phase)} · {humanise(dec.outcome)}
              {dec.approval_tier ? ` · ${humanise(dec.approval_tier)}` : ""} · <When at={dec.evaluated_at} />
            </p>
            <ChecksTable checks={dec.results} caption={`Eligibility checks (${dec.phase})`} />
          </div>
        ))}
      </Section>
      <Section id="approvals" title="Approvals">
        <TableShell caption="Approval decisions" head={["Round", "Approver", "Decision", "Reason", "Decided"]} minWidthClass="min-w-[48rem]">
          {d.approvals.map((a, i) => (
            <tr key={i} className="border-b border-line align-top last:border-b-0">
              <td className="p-3">{text(a.approval_round)}</td>
              <th scope="row" className="p-3 font-normal">
                <Code>{text(a.approver_id)}</Code>
              </th>
              <td className="p-3">{text(a.decision)}</td>
              <td className="p-3 text-sm">{text(a.reason)}</td>
              <td className="p-3 text-sm">{text(a.decided_at)}</td>
            </tr>
          ))}
        </TableShell>
      </Section>
      <Section id="attempts" title="Provider attempts">
        <TableShell caption="Provider attempts" head={["#", "Operation", "Classification", "Provider status", "Started"]} minWidthClass="min-w-[48rem]">
          {d.attempts.map((a, i) => (
            <tr key={i} className="border-b border-line align-top last:border-b-0">
              <td className="p-3">{text(a.attempt_no)}</td>
              <th scope="row" className="p-3 font-normal">
                {humanise(text(a.operation))}
              </th>
              <td className="p-3">
                {humanise(text(a.classification))}
                {a.unknown_reason ? <span className="block text-sm">{text(a.unknown_reason)}</span> : null}
              </td>
              <td className="p-3 text-sm">{text(a.provider_status_raw)}</td>
              <td className="p-3 text-sm">{text(a.started_at)}</td>
            </tr>
          ))}
        </TableShell>
      </Section>
      <Section id="events" title="Event history">
        <TableShell caption="Payout events" head={["Recorded", "Event", "Transition", "Source", "Applied"]} minWidthClass="min-w-[56rem]">
          {d.events.map((e, i) => (
            <tr key={i} className="border-b border-line align-top last:border-b-0">
              <td className="p-3 text-sm">{text(e.recorded_at)}</td>
              <th scope="row" className="p-3 font-normal">
                {humanise(text(e.event_type))}
                {e.reason ? <span className="block text-sm">{text(e.reason)}</span> : null}
              </th>
              <td className="p-3 text-sm">
                {e.to_status ? `${text(e.from_status)} → ${text(e.to_status)}` : "—"} {e.transition_code ? `(${text(e.transition_code)})` : ""}
              </td>
              <td className="p-3 text-sm">{text(e.source)}</td>
              <td className="p-3 text-sm">{e.applied === false ? `No: ${text(e.not_applied_reason)}` : "Yes"}</td>
            </tr>
          ))}
        </TableShell>
      </Section>
      <Section id="exceptions" title="Exceptions">
        {d.exceptions.length === 0 ? (
          <p className="text-ink-700">None.</p>
        ) : (
          <ul className="space-y-2">
            {d.exceptions.map((e) => (
              <li key={e.id}>
                {humanise(e.exception_type)} · {e.severity} · <StatusBadge status={e.status} />
              </li>
            ))}
          </ul>
        )}
      </Section>
    </AccountPage>
  );
}
