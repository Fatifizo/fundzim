import Link from "next/link";
import type { ReactNode } from "react";

import { formatDateTime } from "@/components/account/format";
import { inputClasses } from "@/components/forms/text-field";
import { Badge } from "@/components/ui/badge";
import { ButtonLink, buttonClasses } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { humanise, statusTone } from "@/lib/payments/labels";
import { formatPaymentMoney } from "@/lib/payments/money";
import type { AdminAttempt, AdminIntent, InboxItem, LiveExecution, PaymentEvent, PaymentMoney, WebhookRejection } from "@/lib/payments/types";

/**
 * Shared, server-renderable pieces of the staff payment pages (Stage 8). Every value from the API is rendered as
 * escaped text. Amounts are formatted from minor-unit strings without floating point (lib/payments/money.ts).
 */

export const PAYMENTS_BASE = "/admin/payments";
export const attemptHref = (id: string) => `${PAYMENTS_BASE}/attempts/${encodeURIComponent(id)}`;
export const intentHref = (id: string) => `${PAYMENTS_BASE}/intents/${encodeURIComponent(id)}`;
export const webhookHref = (id: string) => `${PAYMENTS_BASE}/webhooks/${encodeURIComponent(id)}`;

/** Every simulated record carries this label (contract §9). */
export function SimulatedBadge({ simulated }: { simulated: boolean }) {
  if (!simulated) return null;
  return (
    <Badge tone="gold" className="border-gold-800 uppercase">
      Simulated — development only
    </Badge>
  );
}

export function StatusBadge({ status }: { status: string | null | undefined }) {
  if (!status) return <span>—</span>;
  return <Badge tone={statusTone(status)}>{humanise(status)}</Badge>;
}

/** Visual "US$12.50" with an accessible "12.50 US dollars". */
export function Amount({ money }: { money: PaymentMoney | null | undefined }) {
  const f = formatPaymentMoney(money);
  return (
    <span>
      <span aria-hidden="true">{f.text}</span>
      <span className="sr-only">{f.accessibleText}</span>
    </span>
  );
}

/** An identifier or raw provider value, verbatim. */
export function Code({ children }: { children: ReactNode }) {
  return <code className="font-mono text-sm break-all text-ink-900">{children}</code>;
}

export function When({ at }: { at: string | null | undefined }) {
  return <>{formatDateTime(at)}</>;
}

export function Section({ id, title, children, description }: { id: string; title: string; description?: ReactNode; children: ReactNode }) {
  return (
    <section aria-labelledby={id} className="space-y-3">
      <h2 id={id} className="font-display text-2xl font-semibold text-ink-900">
        {title}
      </h2>
      {description ? <div className="text-ink-700">{description}</div> : null}
      {children}
    </section>
  );
}

export function Facts({ items }: { items: Array<[string, ReactNode]> }) {
  return (
    <dl className="grid gap-3 rounded-card border border-line bg-surface p-4 sm:grid-cols-2">
      {items.map(([label, value]) => (
        <div key={label} className="min-w-0">
          <dt className="text-sm font-semibold text-ink-600">{label}</dt>
          <dd className="break-words text-ink-900">{value ?? "—"}</dd>
        </div>
      ))}
    </dl>
  );
}

/** `minWidthClass` must be a literal Tailwind class (no inline styles: the CSP forbids style attributes). */
export function TableShell({ caption, head, children, minWidthClass = "min-w-[44rem]" }: { caption: string; head: string[]; children: ReactNode; minWidthClass?: string }) {
  return (
    <div className="overflow-x-auto rounded-card border border-line bg-surface" role="region" aria-label={caption} tabIndex={0}>
      <table className={`w-full text-left text-ink-700 ${minWidthClass}`}>
        <caption className="sr-only">{caption}</caption>
        <thead>
          <tr className="border-b border-line text-sm text-ink-600">
            {head.map((h) => (
              <th key={h} scope="col" className="p-3">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  );
}

const rowClass = "border-b border-line align-top last:border-b-0";
const cell = "p-3";

/** GET filter form (like the review queue). `fields` are selects; empty value = any. */
export function FilterForm({
  action,
  label,
  fields,
}: {
  action: string;
  label: string;
  fields: Array<{ name: string; label: string; value: string | undefined; options: ReadonlyArray<{ value: string; label: string }> }>;
}) {
  return (
    <form method="get" action={action} aria-label={label} className="grid gap-4 rounded-card border border-line bg-surface p-4 sm:grid-cols-3 sm:items-end">
      {fields.map((f) => (
        <div key={f.name} className="space-y-1">
          <label htmlFor={`filter-${f.name}`} className="block font-semibold text-ink-900">
            {f.label}
          </label>
          <select id={`filter-${f.name}`} name={f.name} defaultValue={f.value ?? ""} className={inputClasses}>
            <option value="">Any</option>
            {f.options.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </div>
      ))}
      <div>
        <button type="submit" className={buttonClasses("primary")}>
          Apply filters
        </button>
      </div>
    </form>
  );
}

export const toOptions = (values: readonly string[]) => values.map((v) => ({ value: v, label: humanise(v) }));

/** Keyset pagination: a "Next page" link that keeps the filters. */
export function NextPage({ base, query, cursorName = "cursor", next, label = "Next page" }: { base: string; query: Record<string, string | undefined>; cursorName?: string; next: string | null; label?: string }) {
  if (!next) return null;
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) if (v) q.set(k, v);
  q.set(cursorName, next);
  return (
    <ButtonLink href={`${base}?${q.toString()}`} variant="outline">
      {label}
    </ButtonLink>
  );
}

export function LiveExecutionPanel({ live, sandboxEnabled }: { live: LiveExecution; sandboxEnabled: boolean }) {
  const yesNo = (b: boolean) => (b ? "true" : "false");
  return (
    <div className="space-y-3 rounded-card border-2 border-danger-700/40 bg-surface p-5">
      <p className="text-lg font-semibold text-ink-900">
        Live payment execution:{" "}
        <span className={live.enabled ? "text-danger-700" : "text-ink-900"}>{live.enabled ? "ENABLED (reported by the API — check the gates below)" : "DISABLED"}</span>
      </p>
      <p className="text-ink-700">No real provider can be called while any gate is off. Stage 8 runs the sandbox simulator only; no real money moves.</p>
      <dl className="grid gap-2 sm:grid-cols-3">
        <div>
          <dt className="text-sm font-semibold text-ink-600">Gate 1 — code constant</dt>
          <dd>
            <Code>constant = {yesNo(live.constant)}</Code>
          </dd>
        </div>
        <div>
          <dt className="text-sm font-semibold text-ink-600">Gate 2 — kill switch</dt>
          <dd>
            <Code>kill_switch = {yesNo(live.kill_switch)}</Code>
          </dd>
        </div>
        <div>
          <dt className="text-sm font-semibold text-ink-600">Effective</dt>
          <dd>
            <Code>enabled = {yesNo(live.enabled)}</Code>
          </dd>
        </div>
      </dl>
      <p className="text-ink-700">
        Sandbox simulator: <strong>{sandboxEnabled ? "enabled (development only)" : "disabled"}</strong>
      </p>
    </div>
  );
}

export function AttemptsTable({ items, caption, empty = "No payments match" }: { items: AdminAttempt[]; caption: string; empty?: string }) {
  if (items.length === 0) return <EmptyState title={empty} description="Change the filters or check again later." />;
  return (
    <TableShell caption={caption} head={["Payment", "Status", "Amount", "Method", "Provider", "Created"]}>
      {items.map((a) => (
        <tr key={a.id} className={rowClass}>
          <th scope="row" className={`${cell} font-semibold`}>
            <Link href={attemptHref(a.id)} className="text-brand-700 underline">
              <Code>{a.id}</Code>
            </Link>
            <span className="block text-sm font-normal text-ink-600">Attempt {a.attempt_number}</span>
            <span className="mt-1 block">
              <SimulatedBadge simulated={a.simulated} />
            </span>
          </th>
          <td className={cell}>
            <StatusBadge status={a.status} />
            {a.status === "UNKNOWN" && a.unknown_reason ? <span className="mt-1 block text-sm">Reason: {humanise(a.unknown_reason)}</span> : null}
            {a.unknown_since ? <span className="block text-sm">Unknown since <When at={a.unknown_since} /></span> : null}
            {a.needs_reconciliation ? (
              <span className="mt-1 block">
                <Badge tone="gold">Needs reconciliation</Badge>
              </span>
            ) : null}
            {a.failure_code ? <span className="block text-sm">Failure: {a.failure_code}</span> : null}
          </td>
          <td className={cell}>
            <Amount money={a.amount} />
          </td>
          <td className={cell}>{humanise(a.method)}</td>
          <td className={cell}>{a.provider.display_name}</td>
          <td className={cell}>
            <When at={a.created_at} />
          </td>
        </tr>
      ))}
    </TableShell>
  );
}

export function IntentsTable({ items, caption }: { items: AdminIntent[]; caption: string }) {
  if (items.length === 0) return <EmptyState title="No donation intents match" description="Change the filters or check again later." />;
  return (
    <TableShell caption={caption} head={["Donation intent", "Status", "Amount", "Campaign", "Anonymous", "Created"]}>
      {items.map((i) => (
        <tr key={i.id} className={rowClass}>
          <th scope="row" className={`${cell} font-semibold`}>
            <Link href={intentHref(i.id)} className="text-brand-700 underline">
              <Code>{i.id}</Code>
            </Link>
            <span className="mt-1 block">
              <SimulatedBadge simulated={i.simulated} />
            </span>
          </th>
          <td className={cell}>
            <StatusBadge status={i.status} />
          </td>
          <td className={cell}>
            <Amount money={i.amount} />
          </td>
          <td className={cell}>
            <Code>{i.campaign_id || "—"}</Code>
          </td>
          <td className={cell}>{i.is_anonymous ? "Yes" : "No"}</td>
          <td className={cell}>
            <When at={i.created_at} />
          </td>
        </tr>
      ))}
    </TableShell>
  );
}

export function InboxTable({ items, caption, empty = "No webhooks match" }: { items: InboxItem[]; caption: string; empty?: string }) {
  if (items.length === 0) return <EmptyState title={empty} description="Verified webhooks appear here once they are stored." />;
  return (
    <TableShell caption={caption} head={["Webhook", "Status", "Class / raw type", "Our reference", "Attempts", "Received"]}>
      {items.map((w) => (
        <tr key={w.id} className={rowClass}>
          <th scope="row" className={`${cell} font-semibold`}>
            <Link href={webhookHref(w.id)} className="text-brand-700 underline">
              <Code>{w.provider_event_id || w.id}</Code>
            </Link>
            <span className="block text-sm font-normal text-ink-600">{w.provider.display_name}</span>
            <span className="mt-1 block">
              <SimulatedBadge simulated={w.simulated} />
            </span>
          </th>
          <td className={cell}>
            <StatusBadge status={w.status} />
            {w.last_error_code ? <span className="block text-sm">Last error: {w.last_error_code}</span> : null}
          </td>
          <td className={cell}>
            {humanise(w.event_class)}
            <span className="block">
              <Code>{w.event_type_raw || "—"}</Code>
            </span>
          </td>
          <td className={cell}>
            <Code>{w.our_reference ?? "—"}</Code>
          </td>
          <td className={cell}>{w.attempts}</td>
          <td className={cell}>
            <When at={w.received_at} />
          </td>
        </tr>
      ))}
    </TableShell>
  );
}

export function RejectionsTable({ items }: { items: WebhookRejection[] }) {
  if (items.length === 0) return <EmptyState title="No rejected webhooks" description="Requests that failed verification are listed here (no payloads are stored)." />;
  return (
    <TableShell caption="Rejected webhook requests" head={["Received", "Provider", "Reason", "Body size", "Body SHA-256"]}>
      {items.map((r) => (
        <tr key={r.id} className={rowClass}>
          <td className={cell}>
            <When at={r.received_at} />
          </td>
          <td className={cell}>
            <Code>{r.provider_code || "—"}</Code>
          </td>
          <td className={cell}>
            <Badge tone="gold">{humanise(r.reason_code)}</Badge>
          </td>
          <td className={cell}>{r.body_bytes === null ? "—" : `${r.body_bytes} bytes`}</td>
          <td className={cell}>
            <Code>{r.body_sha256 ?? "—"}</Code>
          </td>
        </tr>
      ))}
    </TableShell>
  );
}

/** The append-only event timeline: applied and not-applied events (with the reason), oldest first as sent. */
export function EventTimeline({ events }: { events: PaymentEvent[] }) {
  if (events.length === 0) return <EmptyState title="No events recorded" />;
  return (
    <ol className="space-y-3" aria-label="Event timeline">
      {events.map((e) => (
        <li key={e.id} className="space-y-1 rounded-card border border-line bg-surface p-4">
          <div className="flex flex-wrap items-center gap-2">
            {e.applied ? <Badge tone="brand">Applied</Badge> : <Badge tone="gold">Not applied{e.not_applied_reason ? `: ${humanise(e.not_applied_reason)}` : ""}</Badge>}
            <span className="font-semibold text-ink-900">
              {e.from_status ? humanise(e.from_status) : "(start)"} → {e.to_status ? humanise(e.to_status) : "—"}
            </span>
            <span className="text-sm text-ink-600">
              {humanise(e.subject_type)} · source {e.source || "—"} · actor {humanise(e.actor_type)}
            </span>
          </div>
          <dl className="grid gap-x-6 gap-y-1 text-sm sm:grid-cols-2">
            {e.raw_status !== null ? (
              <div>
                <dt className="inline font-semibold text-ink-600">Raw provider status: </dt>
                <dd className="inline">
                  <Code>{e.raw_status}</Code>
                </dd>
              </div>
            ) : null}
            {e.provider_event_id ? (
              <div>
                <dt className="inline font-semibold text-ink-600">Provider event: </dt>
                <dd className="inline">
                  <Code>{e.provider_event_id}</Code>
                </dd>
              </div>
            ) : null}
            {e.transition_code ? (
              <div>
                <dt className="inline font-semibold text-ink-600">Transition: </dt>
                <dd className="inline">
                  <Code>{e.transition_code}</Code>
                </dd>
              </div>
            ) : null}
            {e.reason ? (
              <div className="sm:col-span-2">
                <dt className="inline font-semibold text-ink-600">Reason: </dt>
                <dd className="inline break-words">{e.reason}</dd>
              </div>
            ) : null}
            <div>
              <dt className="inline font-semibold text-ink-600">Occurred: </dt>
              <dd className="inline">
                <When at={e.occurred_at} />
              </dd>
            </div>
            <div>
              <dt className="inline font-semibold text-ink-600">Recorded: </dt>
              <dd className="inline">
                <When at={e.recorded_at} />
              </dd>
            </div>
            {e.correlation_id ? (
              <div className="sm:col-span-2">
                <dt className="inline font-semibold text-ink-600">Correlation id: </dt>
                <dd className="inline">
                  <Code>{e.correlation_id}</Code>
                </dd>
              </div>
            ) : null}
          </dl>
        </li>
      ))}
    </ol>
  );
}
