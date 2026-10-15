import Link from "next/link";
import type { ReactNode } from "react";

import { FINANCE_BASE, LedgerAmount } from "@/components/admin/finance/common";
import { StatusBadge, TableShell, When } from "@/components/admin/payments/common";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import { basisLabel, describeTerms, responsibilityLabel } from "@/lib/fees/labels";
import type { Adjustment, Assessment, FeePolicy, FeeQuote, ProviderSchedule } from "@/lib/fees/types";
import { humanise } from "@/lib/payments/labels";

export const FEES_BASE = `${FINANCE_BASE}/fees`;
export const policyHref = (id: string) => `${FEES_BASE}/policies/${encodeURIComponent(id)}`;
export const assessmentHref = (id: string) => `${FEES_BASE}/assessments/${encodeURIComponent(id)}`;

/** Every fee page: TEST fees of synthetic configuration; production fee charging and revenue are BLOCKED. */
export function FeeBanner() {
  return (
    <div className="rounded-card border-2 border-gold-500/50 bg-gold-100 p-4">
      <p className="text-lg font-semibold text-ink-900">
        TEST FEES ONLY <Badge tone="gold">TEST</Badge>
      </p>
      <p className="text-ink-800">
        TEST — synthetic configuration, not FundZim&apos;s commercial rates. No fee is charged to anyone and no real revenue is recognised. Production
        fees: BLOCKED.
      </p>
    </div>
  );
}

/** A fee amount; null is UNKNOWN (never shown as zero). */
export function FeeAmount({ minor, currency }: { minor: string | null; currency: string }) {
  if (minor === null) return <Badge tone="info">UNKNOWN</Badge>;
  return <LedgerAmount minor={minor} currency={currency} />;
}

const BASIS_TONE: Record<string, "brand" | "info" | "gold" | "neutral"> = { QUOTED: "brand", ESTIMATED: "info", UNKNOWN: "gold", NOT_CONFIGURED: "neutral" };
export function BasisBadge({ basis }: { basis: string }) {
  return (
    <Badge tone={BASIS_TONE[basis] ?? "neutral"}>
      <span aria-hidden="true">{basis}</span>
      <span className="sr-only">{basisLabel(basis)}</span>
    </Badge>
  );
}

export function PoliciesTable({ items, caption }: { items: FeePolicy[]; caption: string }) {
  if (items.length === 0) return <EmptyState title="No fee policies yet" description="Create a policy scope, then draft its first version." />;
  return (
    <TableShell caption={caption} head={["Policy", "Category", "Currency", "Scope", "Versions", "In force"]} minWidthClass="min-w-[56rem]">
      {items.map((p) => {
        const active = p.versions.find((v) => v.id === p.active_version_id);
        return (
          <tr key={p.id} className="border-b border-line align-top last:border-b-0">
            <th scope="row" className="p-3 font-normal">
              <Link href={policyHref(p.id)} className="text-brand-700 underline">
                {p.name}
              </Link>{" "}
              <span className="block text-sm text-ink-600">{p.code}</span>
            </th>
            <td className="p-3">{humanise(p.category)}</td>
            <td className="p-3">{p.currency}</td>
            <td className="p-3 text-sm">{scopeText(p)}</td>
            <td className="p-3 text-sm">{p.versions.map((v) => `v${v.version_no} ${humanise(v.status)}`).join(", ") || "—"}</td>
            <td className="p-3 text-sm">{active ? `v${active.version_no}: ${describeTerms(active.terms, p.currency)}` : "None"}</td>
          </tr>
        );
      })}
    </TableShell>
  );
}

export function scopeText(p: FeePolicy): string {
  const parts = [p.payment_method && `method ${p.payment_method}`, p.campaign_category && `category ${p.campaign_category}`, p.campaign_id && `campaign ${p.campaign_id}`, p.provider_id && `provider ${p.provider_id}`].filter(Boolean);
  return parts.length ? parts.join(", ") : "Platform-wide";
}

export function SchedulesTable({ items, caption, actions }: { items: ProviderSchedule[]; caption: string; actions?: (s: ProviderSchedule) => ReactNode }) {
  if (items.length === 0) return <EmptyState title="No provider fee evidence recorded" description="Provider fees are UNKNOWN until evidence is recorded." />;
  return (
    <TableShell caption={caption} head={["Provider", "Fee type", "Terms", "Evidence", "Status", "Actions"]} minWidthClass="min-w-[60rem]">
      {items.map((s) => (
        <tr key={s.id} className="border-b border-line align-top last:border-b-0">
          <th scope="row" className="p-3 font-normal text-sm">
            {s.provider_is_sandbox ? "Sandbox simulator" : s.provider_id}
            <span className="block text-ink-600">
              {s.contract_reference} · {s.schedule_version}
            </span>
          </th>
          <td className="p-3 text-sm">
            {humanise(s.fee_type)} · {s.currency}
            {s.payment_method ? ` · ${s.payment_method}` : ""}
          </td>
          <td className="p-3 text-sm">
            {describeTerms(s.terms, s.currency)}
            {!s.rounding_known ? <span className="block">Provider rounding unknown</span> : null}
          </td>
          <td className="p-3 text-sm">
            <Badge tone={s.confirmation_status === "SYNTHETIC" ? "gold" : "info"}>{s.confirmation_status}</Badge>
            <span className="block">{s.evidence_source}</span>
          </td>
          <td className="p-3">
            <StatusBadge status={s.status} />
          </td>
          <td className="space-y-2 p-3">{actions ? actions(s) : null}</td>
        </tr>
      ))}
    </TableShell>
  );
}

export function QuotesTable({ items, caption }: { items: FeeQuote[]; caption: string }) {
  if (items.length === 0) return <EmptyState title="No fee quotes yet" />;
  return (
    <TableShell caption={caption} head={["Created", "Donation", "Platform fee", "Provider fee", "Donor charge", "Status"]} minWidthClass="min-w-[56rem]">
      {items.map((q) => (
        <tr key={q.id} className="border-b border-line align-top last:border-b-0">
          <td className="p-3">
            <When at={q.created_at} />
          </td>
          <th scope="row" className="p-3 font-normal">
            <LedgerAmount minor={q.amount_minor} currency={q.currency} /> <Badge tone="gold">TEST</Badge>
            {q.system_generated ? <span className="block text-sm text-ink-600">Computed at intent creation</span> : null}
          </th>
          <td className="p-3">
            <LedgerAmount minor={q.platform_fee_minor} currency={q.currency} /> <BasisBadge basis={q.platform_fee_basis} />
          </td>
          <td className="p-3">
            <FeeAmount minor={q.provider_fee_minor} currency={q.currency} /> {q.provider_fee_minor !== null ? <BasisBadge basis={q.provider_fee_basis} /> : null}
          </td>
          <td className="p-3">
            <FeeAmount minor={q.donor_charge_minor} currency={q.currency} />
          </td>
          <td className="p-3">
            <StatusBadge status={q.status} />
          </td>
        </tr>
      ))}
    </TableShell>
  );
}

export function AssessmentsTable({ items, caption, staff = true }: { items: Assessment[]; caption: string; staff?: boolean }) {
  if (items.length === 0) return <EmptyState title="No fees assessed yet" />;
  return (
    <TableShell caption={caption} head={["Assessed", "Fee", "Category", "Basis", "Borne by", "Accounting"]} minWidthClass="min-w-[60rem]">
      {items.map((a) => (
        <tr key={a.id} className="border-b border-line align-top last:border-b-0">
          <td className="p-3">
            <When at={a.assessed_at} />
          </td>
          <th scope="row" className="p-3 font-normal">
            {staff ? (
              <Link href={assessmentHref(a.id)} className="text-brand-700 underline">
                <FeeAmount minor={a.fee_minor} currency={a.currency} />
              </Link>
            ) : (
              <FeeAmount minor={a.fee_minor} currency={a.currency} />
            )}{" "}
            {a.synthetic ? <Badge tone="gold">TEST_ONLY synthetic</Badge> : <Badge tone="gold">TEST payment</Badge>}
            <span className="block text-sm text-ink-600">
              on <LedgerAmount minor={a.basis_minor} currency={a.currency} />
              {a.net_fee_minor !== null && a.net_fee_minor !== a.fee_minor ? (
                <>
                  {" "}
                  · after adjustments <LedgerAmount minor={a.net_fee_minor} currency={a.currency} />
                </>
              ) : null}
            </span>
          </th>
          <td className="p-3 text-sm">{humanise(a.category)}</td>
          <td className="p-3">
            <BasisBadge basis={a.basis} />
          </td>
          <td className="p-3 text-sm">{responsibilityLabel(a.responsibility)}</td>
          <td className="p-3 text-sm">{humanise(a.accounting_status)}</td>
        </tr>
      ))}
    </TableShell>
  );
}

/** staff=false (organiser view) hides staff-only notes and actions. */
export function AdjustmentsTable({ items, caption, actions, staff = true }: { items: Adjustment[]; caption: string; actions?: (a: Adjustment) => ReactNode; staff?: boolean }) {
  if (items.length === 0) return <EmptyState title="No fee adjustments" />;
  const head = staff ? ["Created", "Amount", "Reason", "Status", "Note", "Actions"] : ["Created", "Amount", "Reason", "Status"];
  return (
    <TableShell caption={caption} head={head} minWidthClass={staff ? "min-w-[60rem]" : "min-w-[40rem]"}>
      {items.map((a) => (
        <tr key={a.id} className="border-b border-line align-top last:border-b-0">
          <td className="p-3">
            <When at={a.created_at} />
          </td>
          <th scope="row" className="p-3 font-normal">
            {a.direction === "INCREASE" ? "+" : "−"}
            <LedgerAmount minor={a.amount_minor} currency={a.currency} />
            <span className="block text-sm text-ink-600">{a.direction === "INCREASE" ? "Fee increased" : "Fee reduced"}</span>
          </th>
          <td className="p-3 text-sm">{humanise(a.reason)}</td>
          <td className="p-3">
            <StatusBadge status={a.status} />
          </td>
          {staff ? (
            <>
              <td className="p-3 text-sm">{a.note}</td>
              <td className="space-y-2 p-3">{actions ? actions(a) : null}</td>
            </>
          ) : null}
        </tr>
      ))}
    </TableShell>
  );
}
