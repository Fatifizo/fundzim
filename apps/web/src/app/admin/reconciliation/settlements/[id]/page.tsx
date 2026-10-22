import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { Facts, Section, StatusBadge } from "@/components/admin/payments/common";
import { LineTable, Money, ReconTabs, TestEvidenceBanner } from "@/components/admin/reconciliation/recon-view";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readBatch } from "@/lib/reconciliation/admin";

export const metadata: Metadata = { title: "Settlement batch", robots: { index: false, follow: false, nocache: true } };

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export default async function ReconciliationSettlementPage({ params }: PageProps<"/admin/reconciliation/settlements/[id]">) {
  const { id } = await params;
  if (!UUID.test(id)) notFound();
  await requireStaff();
  const raw = (await serverGetOr404<unknown>(`/api/v1/admin/reconciliation/settlements/${id}`, `/admin/reconciliation/settlements/${id}`)).data;
  const b = readBatch(typeof raw === "object" && raw !== null ? (raw as Record<string, unknown>) : {});
  return (
    <AccountPage title={`Settlement batch ${b.ref}`} intro={`${b.sourceCode} · ${b.currency} · ${b.date}`}>
      <TestEvidenceBanner />
      <ReconTabs current="/admin/reconciliation/settlements" />
      <Section id="header" title="Header and expectation">
        <Facts items={[
          ["Status", <StatusBadge key="s" status={b.status} />],
          ["Gross credits", <Money key="c" minor={b.credits} currency={b.currency} />],
          ["Gross debits", <Money key="d" minor={b.debits} currency={b.currency} />],
          ["Provider fees", <Money key="f" minor={b.fees} currency={b.currency} />],
          ["Net reported by the provider", <Money key="n" minor={b.net} currency={b.currency} />],
          ["Net FundZim expects", <Money key="e" minor={b.expectedNet} currency={b.currency} />],
          ["Lines settled", `${b.settled} of ${b.lines}`],
          ["Import", <Link key="i" href={`/admin/reconciliation/batches/${b.importId}`} className="text-brand-700 underline">View the statement</Link>],
        ]} />
      </Section>
      <Section id="lines" title="Lines">
        <LineTable lines={b.items} caption="Settlement batch lines" />
      </Section>
    </AccountPage>
  );
}
