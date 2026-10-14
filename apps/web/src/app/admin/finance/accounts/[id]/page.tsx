import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { FINANCE_BASE, LedgerAmount, LedgerEnvironmentBanner, TestOnlyBadge } from "@/components/admin/finance/common";
import { LedgerAction } from "@/components/admin/finance/ledger-action";
import { Code, Facts, Section, StatusBadge, TableShell, When } from "@/components/admin/payments/common";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { isUuid } from "@/lib/campaigns/paths";
import { readAccount, readJournal, readPage } from "@/lib/ledger/normalise";
import { humanise } from "@/lib/payments/labels";

export const metadata: Metadata = { title: "Ledger account", robots: { index: false, follow: false, nocache: true } };

/** One account: balance, totals, status controls (`ledger.account.manage` + step-up) and its journal activity. */
export default async function LedgerAccountPage({ params }: PageProps<"/admin/finance/accounts/[id]">) {
  await requireStaff();
  const { id } = await params;
  if (!isUuid(id)) notFound();
  const result = await serverGetOr404<unknown>(`/api/v1/admin/ledger/accounts/${id}`, `${FINANCE_BASE}/accounts/${id}`);
  const d = (typeof result.data === "object" && result.data !== null ? result.data : {}) as Record<string, unknown>;
  const a = readAccount(d.account);
  if (!a) notFound();
  const activity = readPage(d.activity, readJournal);
  return (
    <AccountPage title="Ledger account" intro={<Link href={`${FINANCE_BASE}/accounts`}>All accounts</Link>}>
      <LedgerEnvironmentBanner environment={a.environment} />
      <Section id="account-title" title={a.code}>
        <Facts
          items={[
            ["Balance (normal-balance terms)", <LedgerAmount key="b" minor={a.balance_minor} currency={a.currency} />],
            ["Total debits", <LedgerAmount key="d" minor={a.debit_total_minor} currency={a.currency} />],
            ["Total credits", <LedgerAmount key="c" minor={a.credit_total_minor} currency={a.currency} />],
            ["Type / normal balance", `${humanise(a.type)} / ${humanise(a.normal_balance)}`],
            ["Kind", `${humanise(a.kind)}${a.non_negative ? " (may never go below zero)" : ""}`],
            ["Owner", a.owner_id ? <Code key="o">{`${humanise(a.owner_type)} ${a.owner_id}`}</Code> : "Platform"],
            ["Status", <StatusBadge key="s" status={a.status} />],
            ["Status reason", a.status_reason ?? "—"],
          ]}
        />
        <div className="flex flex-wrap gap-3">
          {/* always mounted: the confirmation notice must survive the refresh that changes the status */}
          <LedgerAction target={{ kind: "account", id: a.id, action: "freeze" }} available={a.status === "ACTIVE"} />
          <LedgerAction target={{ kind: "account", id: a.id, action: "unfreeze" }} available={a.status === "FROZEN"} />
          <LedgerAction target={{ kind: "account", id: a.id, action: "close" }} available={a.status !== "CLOSED"} />
        </div>
      </Section>
      <Section id="activity-title" title="Activity">
        {activity.items.length === 0 ? (
          <p className="text-ink-700">No journals.</p>
        ) : (
          <TableShell caption="Account activity" head={["Posted", "Rule", "Journal total", "Source"]} minWidthClass="min-w-[44rem]">
            {activity.items.map((j) => (
              <tr key={j.id} className="border-b border-line align-top last:border-b-0">
                <td className="p-3">
                  <When at={j.posted_at} />
                </td>
                <th scope="row" className="p-3 font-normal">
                  <Link href={`${FINANCE_BASE}/journals/${encodeURIComponent(j.id)}`} className="text-brand-700 underline">
                    {j.posting_rule}
                  </Link>{" "}
                  <TestOnlyBadge testOnly={j.test_only} />
                </th>
                <td className="p-3">
                  <LedgerAmount minor={j.total_minor} currency={j.currency} />
                </td>
                <td className="p-3 text-sm">
                  {j.source_system} · {j.source_type}
                </td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
    </AccountPage>
  );
}
