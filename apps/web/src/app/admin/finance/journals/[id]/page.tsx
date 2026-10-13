import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { FINANCE_BASE, LedgerAmount, LedgerEnvironmentBanner, TestOnlyBadge } from "@/components/admin/finance/common";
import { LedgerAction } from "@/components/admin/finance/ledger-action";
import { Code, Facts, Section, StatusBadge, TableShell, When } from "@/components/admin/payments/common";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { isUuid } from "@/lib/campaigns/paths";
import { readJournalDetail } from "@/lib/ledger/normalise";
import { humanise } from "@/lib/payments/labels";

export const metadata: Metadata = { title: "Journal", robots: { index: false, follow: false, nocache: true } };

/** One journal: lines, balance check, source trace, reversal history; a reversal can be REQUESTED (maker-checker). */
export default async function JournalPage({ params }: PageProps<"/admin/finance/journals/[id]">) {
  await requireStaff();
  const { id } = await params;
  if (!isUuid(id)) notFound();
  const result = await serverGetOr404<unknown>(`/api/v1/admin/ledger/journals/${id}`, `${FINANCE_BASE}/journals/${id}`);
  const j = readJournalDetail(result.data);
  if (!j) notFound();
  const href = (tx: string) => `${FINANCE_BASE}/journals/${encodeURIComponent(tx)}`;
  return (
    <AccountPage title="Journal" intro={<Link href={`${FINANCE_BASE}/journals`}>All journals</Link>}>
      <LedgerEnvironmentBanner environment={j.environment} />
      <Section id="journal-title" title={j.posting_rule}>
        <p className="flex flex-wrap gap-2">
          <TestOnlyBadge testOnly={j.test_only} />
          {j.balanced ? <StatusBadge status="PASSED" /> : <StatusBadge status="FAILED" />}
          <span className="text-sm text-ink-700">{j.balanced ? "Debits equal credits" : "NOT BALANCED — report this"}</span>
        </p>
        <Facts
          items={[
            ["Journal id", <Code key="i">{j.id}</Code>],
            ["Rule version", String(j.posting_rule_version)],
            ["Description", j.description],
            ["Source", `${j.source_system} · ${j.source_type}`],
            ["Source id", <Code key="s">{j.source_id}</Code>],
            ["Evidence", humanise(j.evidence_class)],
            ["Idempotency key", <Code key="k">{j.idempotency_key}</Code>],
            ["Occurred / posted", <span key="t"><When at={j.occurred_at} /> / <When at={j.posted_at} /></span>],
            ["Period", `${j.period_code}${j.is_prior_period ? " (prior-period posting)" : ""}`],
            ["Created by", j.created_by],
            ["Reason", j.reason ?? "—"],
            ["Labels", Object.entries(j.metadata).map(([k, v]) => `${k}=${v}`).join(", ") || "—"],
          ]}
        />
      </Section>
      <Section id="lines-title" title="Lines">
        <TableShell caption="Journal lines" head={["#", "Account", "Debit", "Credit"]} minWidthClass="min-w-[44rem]">
          {j.lines.map((l) => (
            <tr key={l.line_no} className="border-b border-line align-top last:border-b-0">
              <td className="p-3">{l.line_no}</td>
              <th scope="row" className="p-3 font-normal">
                <Link href={`${FINANCE_BASE}/accounts/${encodeURIComponent(l.account_id)}`} className="text-brand-700 underline">
                  <Code>{l.account_code}</Code>
                </Link>
              </th>
              <td className="p-3">{l.debit_minor !== "0" ? <LedgerAmount minor={l.debit_minor} currency={l.currency} /> : ""}</td>
              <td className="p-3">{l.credit_minor !== "0" ? <LedgerAmount minor={l.credit_minor} currency={l.currency} /> : ""}</td>
            </tr>
          ))}
        </TableShell>
      </Section>
      <Section id="reversal-title" title="Reversal history">
        {j.reverses_transaction_id ? (
          <p>
            This journal reverses <Link href={href(j.reverses_transaction_id)} className="text-brand-700 underline">the original journal</Link>.
          </p>
        ) : j.reversed_by_transaction_id ? (
          <p>
            Reversed by <Link href={href(j.reversed_by_transaction_id)} className="text-brand-700 underline">this reversal journal</Link>. A journal is
            reversed at most once.
          </p>
        ) : (
          <>
            <p className="text-ink-700">Not reversed.</p>
            {j.posting_rule !== "REVERSAL" ? <LedgerAction target={{ kind: "reversal", transactionId: j.id }} /> : null}
          </>
        )}
      </Section>
      <Section id="sources-title" title="Source trace">
        {j.source_references.length === 0 ? (
          <p className="text-ink-700">No source reference recorded.</p>
        ) : (
          <TableShell caption="Source references" head={["Source event", "Decision", "Evidence", "Observed"]} minWidthClass="min-w-[44rem]">
            {j.source_references.map((s) => (
              <tr key={s.id} className="border-b border-line align-top last:border-b-0">
                <th scope="row" className="p-3 font-normal">
                  <Code>{s.source_event_id}</Code>
                </th>
                <td className="p-3">
                  <StatusBadge status={s.decision} />
                </td>
                <td className="p-3">{humanise(s.evidence_class)}</td>
                <td className="p-3">
                  <When at={s.observed_at} />
                </td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
    </AccountPage>
  );
}
