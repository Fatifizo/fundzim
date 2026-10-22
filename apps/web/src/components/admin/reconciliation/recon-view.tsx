import Link from "next/link";

import { ShortId } from "@/components/admin/operations/ops";
import { Code, StatusBadge, TableShell, When } from "@/components/admin/payments/common";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { formatMinor, type ExceptionItem, type Line, type Run } from "@/lib/reconciliation/admin";
import { humanise } from "@/lib/payments/labels";

/** Severity badge (engineering defaults by type, not regulatory classifications). */
export function Severity({ severity }: { severity: string }) {
  const tone = severity === "CRITICAL" || severity === "HIGH" ? "gold" : "neutral";
  return (
    <Badge tone={tone} className={severity === "CRITICAL" ? "border-gold-800 uppercase" : undefined}>
      {humanise(severity)}
    </Badge>
  );
}

export function Money({ minor, currency }: { minor: string | null; currency: string | null }) {
  return <span className="whitespace-nowrap tabular-nums">{formatMinor(minor, currency)}</span>;
}

/** The banner every reconciliation page carries: TEST evidence only, production BLOCKED. */
export function TestEvidenceBanner() {
  return (
    <Alert tone="info" title="TEST evidence only">
      No payment provider is selected. Every statement here is a staff-supplied file in FundZim&apos;s canonical format or was generated
      from the sandbox simulator — never a real provider, settlement or bank statement. Nothing here is settled money, and production
      imports and settlement postings are blocked.
    </Alert>
  );
}

const TABS: ReadonlyArray<{ href: string; label: string }> = [
  { href: "/admin/reconciliation", label: "Overview" },
  { href: "/admin/reconciliation/batches", label: "Imports and runs" },
  { href: "/admin/reconciliation/exceptions", label: "Exceptions" },
  { href: "/admin/reconciliation/settlements", label: "Settlements" },
  { href: "/admin/reconciliation/payouts", label: "Payouts" },
  { href: "/admin/reconciliation/ledger", label: "Ledger" },
  { href: "/admin/reconciliation/fees", label: "Fees" },
  { href: "/admin/reconciliation/reports", label: "Reports" },
  { href: "/admin/reconciliation/rules", label: "Matching rules" },
];

export function ReconTabs({ current }: { current: string }) {
  return (
    <nav aria-label="Reconciliation sections">
      <ul className="flex flex-wrap gap-2">
        {TABS.map((t) => (
          <li key={t.href}>
            <Link
              href={t.href}
              prefetch={false}
              aria-current={t.href === current ? "page" : undefined}
              className={`inline-flex min-h-11 items-center rounded-full border px-4 text-sm font-semibold ${t.href === current ? "border-brand-700 bg-brand-50 text-brand-800" : "border-line bg-surface text-ink-900 hover:border-brand-700"}`}
            >
              {t.label}
            </Link>
          </li>
        ))}
      </ul>
    </nav>
  );
}

export function ExceptionTable({ items, caption }: { items: ExceptionItem[]; caption: string }) {
  if (items.length === 0) return <p className="text-ink-700">No exceptions.</p>;
  return (
    <TableShell caption={caption} head={["Exception", "Severity", "Status", "Source", "Expected", "Reported", "Age", "Seen"]}>
      {items.map((e) => (
        <tr key={e.id} className="border-b border-line last:border-0">
          <td className="p-3">
            <Link href={`/admin/reconciliation/exceptions/${e.id}`} className="font-semibold text-brand-700 underline">
              {humanise(e.type)}
            </Link>
          </td>
          <td className="p-3">
            <Severity severity={e.severity} />
          </td>
          <td className="p-3">
            <StatusBadge status={e.status} />
          </td>
          <td className="p-3">{e.sourceCode}</td>
          <td className="p-3">
            <Money minor={e.expected} currency={e.currency} />
          </td>
          <td className="p-3">
            <Money minor={e.actual} currency={e.rawCurrency ?? e.currency} />
          </td>
          <td className="p-3">{e.ageHours < 48 ? `${e.ageHours} h` : `${Math.floor(e.ageHours / 24)} days`}</td>
          <td className="p-3">{e.occurrences}×</td>
        </tr>
      ))}
    </TableShell>
  );
}

export function LineTable({ lines, caption }: { lines: Line[]; caption: string }) {
  if (lines.length === 0) return <p className="text-ink-700">No lines.</p>;
  return (
    <TableShell caption={caption} head={["Line", "Kind", "Status", "Amount", "Fee", "References", "Match", "Ledger"]} minWidthClass="min-w-[60rem]">
      {lines.map((l) => (
        <tr key={l.id} className="border-b border-line align-top last:border-0">
          <td className="p-3">
            <Code>{l.lineId}</Code>
            {l.revision > 1 ? <span className="block text-sm text-ink-600">revision {l.revision}</span> : null}
          </td>
          <td className="p-3">
            {humanise(l.kind)} <span className="block text-sm text-ink-600">{humanise(l.direction)}</span>
          </td>
          <td className="p-3">{humanise(l.status)}</td>
          <td className="p-3">
            <Money minor={l.amount} currency={l.currency} />
            {l.rawCurrency !== l.currency ? <span className="block text-sm text-ink-600">reported as {l.rawCurrency}</span> : null}
          </td>
          <td className="p-3">{l.fee ? <Money minor={l.fee} currency={l.currency} /> : "not reported"}</td>
          <td className="p-3 text-sm">
            {l.merchantRef ? <span className="block">ours: <ShortId value={l.merchantRef} /></span> : null}
            {l.providerRef ? <span className="block">provider: <Code>{l.providerRef}</Code></span> : null}
            {l.counterpartyRef ? <span className="block">bank: <Code>{l.counterpartyRef}</Code></span> : null}
          </td>
          <td className="p-3">
            <StatusBadge status={l.matchStatus} />
            {l.match ? (
              <span className="block text-sm text-ink-600">
                {humanise(l.match.confidence)} · {humanise(l.match.subjectType)} <ShortId value={l.match.subjectId} />
              </span>
            ) : null}
          </td>
          <td className="p-3">{l.match ? humanise(l.match.ledgerStatus) : "—"}</td>
        </tr>
      ))}
    </TableShell>
  );
}

export function RunTable({ runs, caption, actions }: { runs: Run[]; caption: string; actions?: (r: Run) => React.ReactNode }) {
  if (runs.length === 0) return <p className="text-ink-700">No runs yet.</p>;
  return (
    <TableShell caption={caption} head={["Queued", "Source", "Currency", "Kind", "Status", "Lines", "Matched", "Opened / closed", "Rule set", ""]} minWidthClass="min-w-[60rem]">
      {runs.map((r) => (
        <tr key={r.id} className="border-b border-line align-top last:border-0">
          <td className="p-3">
            <When at={r.queuedAt} />
          </td>
          <td className="p-3">{r.sourceCode}</td>
          <td className="p-3">{r.currency}</td>
          <td className="p-3">{humanise(r.kind)}</td>
          <td className="p-3">
            <StatusBadge status={r.status} />
            {r.failureCode ? <span className="block text-sm text-ink-600">{humanise(r.failureCode)}</span> : null}
          </td>
          <td className="p-3">{r.considered}</td>
          <td className="p-3">{r.matched}</td>
          <td className="p-3">
            {r.opened} / {r.closed}
          </td>
          <td className="p-3">{r.ruleSetVersion ? `v${r.ruleSetVersion} · ${r.matcherVersion}` : "—"}</td>
          <td className="p-3">{actions && (r.status === "COMPLETED" || r.status === "FAILED") ? actions(r) : null}</td>
        </tr>
      ))}
    </TableShell>
  );
}
