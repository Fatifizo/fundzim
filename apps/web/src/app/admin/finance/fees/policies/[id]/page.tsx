import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { FeeAction } from "@/components/admin/finance/fee-action";
import { VersionForm } from "@/components/admin/finance/fee-forms";
import { FEES_BASE, FeeBanner, scopeText } from "@/components/admin/finance/fees";
import { Facts, Section, StatusBadge, When } from "@/components/admin/payments/common";
import { Badge } from "@/components/ui/badge";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { isUuid } from "@/lib/campaigns/paths";
import { bpsToPercent, describeTerms, responsibilityLabel } from "@/lib/fees/labels";
import { readPolicy } from "@/lib/fees/normalise";
import { humanise } from "@/lib/payments/labels";

export const metadata: Metadata = { title: "Fee policy", robots: { index: false, follow: false, nocache: true } };

/** One fee policy with its versions and their lifecycle actions (maker-checker, step-up). */
export default async function FeePolicyPage({ params }: PageProps<"/admin/finance/fees/policies/[id]">) {
  const { id } = await params;
  if (!isUuid(id)) notFound();
  await requireStaff();
  const p = readPolicy((await serverGetOr404<unknown>(`/api/v1/admin/fees/policies/${id}`, `${FEES_BASE}/policies/${id}`)).data);
  if (!p) notFound();
  const openDraft = p.versions.some((v) => v.status === "DRAFT" || v.status === "PENDING_APPROVAL");
  return (
    <AccountPage title="Fee policy" intro={<Link href={`${FEES_BASE}/policies`}>All fee policies</Link>}>
      <FeeBanner />
      <section aria-labelledby="policy" className="space-y-3">
        <h2 id="policy" className="font-display text-2xl font-semibold text-ink-900">
          {p.name} <Badge tone="gold">TEST</Badge>
        </h2>
        <Facts items={[["Code", p.code], ["Category", humanise(p.category)], ["Transaction", humanise(p.transaction_type)], ["Currency", p.currency], ["Scope", scopeText(p)], ["Version in force", p.active_version_id ? `v${p.versions.find((v) => v.id === p.active_version_id)?.version_no ?? "?"}` : "None"]]} />
        {p.description ? <p className="text-ink-800">{p.description}</p> : null}
      </section>
      <Section id="versions" title="Versions">
        {p.versions.length === 0 ? <p className="text-ink-700">No versions yet.</p> : null}
        <ol className="space-y-4">
          {p.versions.map((v) => (
            <li key={v.id} className="space-y-3 rounded-card border border-line bg-surface p-4">
              <section aria-label={`Version ${v.version_no}`} className="space-y-3">
                <h3 className="flex flex-wrap items-center gap-2 font-display text-xl font-semibold text-ink-900">
                  Version {v.version_no} <StatusBadge status={v.status} /> {v.id === p.active_version_id ? <Badge tone="brand">In force</Badge> : null}
                </h3>
                <Facts
                  items={[
                    ["Terms", describeTerms(v.terms, p.currency)],
                    ["Who bears it", responsibilityLabel(v.responsibility) + (v.donor_share_bps ? ` (donor ${bpsToPercent(v.donor_share_bps)})` : "")],
                    ["Refund treatment", v.refund_treatment ? humanise(v.refund_treatment) : "—"],
                    ["Effective", <span key="e"><When at={v.effective_from} />{v.effective_to ? <> to <When at={v.effective_to} /></> : null}</span>],
                    ["Terms hash", v.terms_hash ? <code key="h" className="break-all text-sm">{v.terms_hash}</code> : "Not frozen (draft)"],
                    ["Approval", v.approved_by ? `Approved: ${v.approval_reason ?? ""}` : v.rejected_by ? `Rejected: ${v.rejection_reason ?? ""}` : "—"],
                  ]}
                />
                <div className="flex flex-wrap gap-3">
                  <FeeAction target={{ kind: "submit", id: v.id }} available={v.status === "DRAFT"} />
                  <FeeAction target={{ kind: "approve", id: v.id, termsHash: v.terms_hash }} available={v.status === "PENDING_APPROVAL"} />
                  <FeeAction target={{ kind: "reject", id: v.id, termsHash: v.terms_hash }} available={v.status === "PENDING_APPROVAL"} />
                  <FeeAction target={{ kind: "activate", id: v.id }} available={v.status === "APPROVED"} />
                  <FeeAction target={{ kind: "suspend", id: v.id }} available={v.status === "ACTIVE"} />
                  <FeeAction target={{ kind: "reactivate", id: v.id }} available={v.status === "SUSPENDED"} />
                  <FeeAction target={{ kind: "retire", id: v.id }} available={["DRAFT", "APPROVED", "ACTIVE", "SUSPENDED"].includes(v.status)} />
                </div>
              </section>
            </li>
          ))}
        </ol>
      </Section>
      <Section id="draft" title="Draft a new version" description="Terms can be edited only as a draft. Submitting freezes them; a different person approves; then the version is activated.">
        <VersionForm policyId={p.id} category={p.category} currency={p.currency} available={!openDraft} />
      </Section>
    </AccountPage>
  );
}
