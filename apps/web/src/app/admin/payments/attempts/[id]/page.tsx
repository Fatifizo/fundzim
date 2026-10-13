import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import {
  Amount,
  Code,
  EventTimeline,
  Facts,
  InboxTable,
  intentHref,
  PAYMENTS_BASE,
  Section,
  SimulatedBadge,
  StatusBadge,
  TableShell,
  When,
} from "@/components/admin/payments/common";
import { ReconList, RefundExecutionDisabledNotice, RefundList } from "@/components/admin/payments/lists";
import { PaymentAction } from "@/components/admin/payments/payment-action";
import { RefundRequestForm } from "@/components/admin/payments/refund-request-form";
import { SandboxWebhookForm } from "@/components/admin/payments/sandbox-panel";
import { Badge } from "@/components/ui/badge";
import { requireStaff, serverGetOptional, serverGetOr404 } from "@/lib/auth/session";
import { isUuid } from "@/lib/campaigns/paths";
import { humanise } from "@/lib/payments/labels";
import { minorUnitsFor } from "@/lib/payments/money";
import { readAttemptDetail, readEvidence, readOverview, readSandboxState } from "@/lib/payments/normalise";
import { REFUNDABLE_PAYMENT_STATUSES } from "@/lib/payments/types";

export const metadata: Metadata = { title: "Payment", robots: { index: false, follow: false, nocache: true } };

/** Statuses for which a provider status query makes sense (non-final). The API decides. */
const QUERYABLE = new Set(["CREATED", "PENDING", "REQUIRES_ACTION", "AUTHORISED", "UNKNOWN"]);

/**
 * Payment detail (`payment.view`): summary, provider calls, the provider's transaction (raw status verbatim and the
 * mapped status), references, session (never its token), webhooks, the append-only event timeline, reconciliation
 * references and refund requests. There is no control here (or anywhere) that marks a payment succeeded or failed.
 */
export default async function PaymentAttemptPage({ params }: PageProps<"/admin/payments/attempts/[id]">) {
  const me = await requireStaff();
  const { id } = await params;
  if (!isUuid(id)) notFound();
  const path = `${PAYMENTS_BASE}/attempts/${id}`;
  const [result, overviewRaw, evidenceRaw] = await Promise.all([
    serverGetOr404<unknown>(`/api/v1/admin/payments/attempts/${id}`, path),
    serverGetOptional<unknown>("/api/v1/admin/payments/overview", path, null),
    serverGetOptional<unknown>(`/api/v1/admin/payments/attempts/${id}/evidence`, path, null),
  ]);
  const evidence = evidenceRaw !== null ? readEvidence(evidenceRaw) : [];
  const a = readAttemptDetail(result.data);
  if (!a) notFound();
  const sandboxEnabled = overviewRaw !== null && readOverview(overviewRaw).sandbox_enabled;
  // Development only: simulator state is fetched only for a simulated payment while the sandbox is enabled; the API
  // answers 404 otherwise (and 403 without payment.sandbox.control), in which case the panel is not shown.
  const sandboxRaw = sandboxEnabled && a.simulated ? await serverGetOptional<unknown>(`/api/v1/admin/payments/sandbox/attempts/${id}`, path, null) : null;
  const sandbox = sandboxRaw !== null ? readSandboxState(sandboxRaw) : null;
  const tx = a.transaction;
  const canRefund = REFUNDABLE_PAYMENT_STATUSES.has(a.status) && a.amount !== null && minorUnitsFor(a.amount.currency) !== null;

  return (
    <AccountPage
      title="Payment"
      intro={
        <span className="flex flex-wrap gap-x-6 gap-y-2 text-base">
          <Link href={`${PAYMENTS_BASE}/attempts`} className="font-semibold text-brand-700 underline">
            Back to payments
          </Link>
          {a.donation_intent_id ? (
            <Link href={intentHref(a.donation_intent_id)} className="font-semibold text-brand-700 underline">
              Donation intent
            </Link>
          ) : null}
        </span>
      }
    >
      <div className="flex flex-wrap items-center gap-2">
        <StatusBadge status={a.status} />
        <SimulatedBadge simulated={a.simulated} />
        {a.needs_reconciliation ? <Badge tone="gold">Needs reconciliation</Badge> : null}
      </div>

      <Section id="summary-title" title="Payment summary">
        <Facts
          items={[
            ["Payment id", <Code key="id">{a.id}</Code>],
            ["Amount", <Amount key="amt" money={a.amount} />],
            ["Method", `${humanise(a.method)} (${humanise(a.method_category)})`],
            ["Provider", a.provider.display_name],
            ["Attempt number", String(a.attempt_number)],
            ["Provider payment reference", <Code key="ppr">{a.provider_payment_ref ?? "—"}</Code>],
            ["Last authoritative source", a.last_authoritative_source ?? "—"],
            ["Unknown since", <When key="us" at={a.unknown_since} />],
            ["Unknown reason", a.unknown_reason ? humanise(a.unknown_reason) : "—"],
            ["Failure code", a.failure_code ?? "—"],
            ["Status polls so far", String(a.poll_attempts)],
            ["Next poll", <When key="np" at={a.next_poll_at} />],
            ["Poll horizon", <When key="ph" at={a.poll_horizon_at} />],
            ["Campaign id", <Code key="c">{a.campaign_id || "—"}</Code>],
            ["Created", <When key="cr" at={a.created_at} />],
            ["Updated", <When key="up" at={a.updated_at} />],
          ]}
        />
        <PaymentAction target={{ kind: "status-query", paymentId: a.id }} available={QUERYABLE.has(a.status)} />
      </Section>

      <Section id="calls-title" title="Provider calls">
        {a.provider_calls.length === 0 ? (
          <p className="text-ink-700">No provider calls recorded.</p>
        ) : (
          <TableShell caption="Provider calls" head={["#", "Operation", "Classification", "Unknown reason", "HTTP", "Raw status", "Latency", "Started"]} minWidthClass="min-w-[52rem]">
            {a.provider_calls.map((c) => (
              <tr key={c.id} className="border-b border-line align-top last:border-b-0">
                <td className="p-3">{c.attempt_no}</td>
                <td className="p-3">{humanise(c.operation)}</td>
                <td className="p-3">
                  <StatusBadge status={c.classification} />
                  {c.error_class ? <span className="block text-sm">{humanise(c.error_class)}</span> : null}
                  {c.error_category ? <span className="block text-sm font-semibold">{humanise(c.error_category)}</span> : null}
                </td>
                <td className="p-3">{c.unknown_reason ? humanise(c.unknown_reason) : "—"}</td>
                <td className="p-3">{c.http_status ?? "—"}</td>
                <td className="p-3">
                  <Code>{c.provider_status_raw ?? "—"}</Code>
                  {c.provider_reference_returned ? (
                    <span className="block text-sm">
                      Ref <Code>{c.provider_reference_returned}</Code>
                    </span>
                  ) : null}
                </td>
                <td className="p-3">{c.latency_ms === null ? "—" : `${c.latency_ms} ms`}</td>
                <td className="p-3">
                  <When at={c.started_at} />
                  {c.correlation_id ? (
                    <span className="block text-sm">
                      <Code>{c.correlation_id}</Code>
                    </span>
                  ) : null}
                </td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>

      <Section id="tx-title" title="Provider transaction" description="The provider's own status is shown exactly as received, next to the status FundZim mapped it to.">
        {tx ? (
          <Facts
            items={[
              ["Raw provider status (verbatim)", <Code key="raw">{tx.raw_status ?? "—"}</Code>],
              ["Mapped status", <StatusBadge key="mapped" status={tx.mapped_status} />],
              ["Mapping version", tx.mapping_version ?? "—"],
              ["Last source", tx.last_source ?? "—"],
              ["Provider transaction reference", <Code key="ref">{tx.provider_transaction_ref ?? "—"}</Code>],
              ["Raw status at", <When key="rsa" at={tx.raw_status_at} />],
              ["Amount reported by the provider", tx.reported_amount ? <Amount key="rep" money={tx.reported_amount} /> : "—"],
              ["Payer instrument (masked)", tx.payer_instrument_masked ?? "—"],
              ["Updated", <When key="u" at={tx.updated_at} />],
            ]}
          />
        ) : (
          <p className="text-ink-700">The provider has not reported a transaction yet.</p>
        )}
      </Section>

      <Section
        id="evidence-title"
        title="Provider evidence"
        description="Every provider observation of this payment, append-only. A contradiction with earlier evidence is escalated to reconciliation; it never changes the payment by itself."
      >
        {evidence.length === 0 ? (
          <p className="text-ink-700">No provider evidence recorded.</p>
        ) : (
          <TableShell caption="Provider evidence" head={["Observed", "Source", "Raw status", "Mapped", "Reported amount", "Consistency", "Environment"]} minWidthClass="min-w-[56rem]">
            {evidence.map((e) => (
              <tr key={e.id} className="border-b border-line align-top last:border-b-0">
                <td className="p-3">
                  <When at={e.observed_at} />
                </td>
                <td className="p-3">{humanise(e.source)}</td>
                <th scope="row" className="p-3 font-normal">
                  <Code>{e.raw_status}</Code>
                  {e.provider_event_id ? (
                    <span className="block text-sm">
                      Event <Code>{e.provider_event_id}</Code>
                    </span>
                  ) : null}
                </th>
                <td className="p-3">
                  <StatusBadge status={e.mapped_status} />
                  {!e.mapped ? <span className="block text-sm">Not mapped</span> : null}
                </td>
                <td className="p-3">{e.reported ? <Amount money={e.reported} /> : "—"}</td>
                <td className="p-3">
                  <StatusBadge status={e.consistency} />
                </td>
                <td className="p-3">{humanise(e.execution_environment)}</td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>

      <Section id="refs-title" title="References">
        {a.references.length === 0 ? (
          <p className="text-ink-700">No provider references recorded.</p>
        ) : (
          <TableShell caption="Provider references" head={["Kind", "Reference", "Source", "Recorded"]} minWidthClass="min-w-[32rem]">
            {a.references.map((r) => (
              <tr key={`${r.ref_kind}:${r.reference}`} className="border-b border-line align-top last:border-b-0">
                <td className="p-3">{humanise(r.ref_kind)}</td>
                <td className="p-3">
                  <Code>{r.reference}</Code>
                </td>
                <td className="p-3">{r.source || "—"}</td>
                <td className="p-3">
                  <When at={r.recorded_at} />
                </td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>

      <Section id="session-title" title="Payment session">
        {a.session ? (
          <Facts
            items={[
              ["Session id", <Code key="sid">{a.session.id}</Code>],
              ["Kind", humanise(a.session.kind)],
              ["Status", <StatusBadge key="s" status={a.session.status} />],
              ["Redirect URL", <Code key="url">{a.session.redirect_url ?? "—"}</Code>],
              ["Expires", <When key="e" at={a.session.expires_at} />],
              ["Donor returned", <When key="r" at={a.session.returned_at} />],
            ]}
          />
        ) : (
          <p className="text-ink-700">No payment session.</p>
        )}
        <p className="text-sm text-ink-600">A donor returning from a hosted page never confirms a payment; it only schedules a status query.</p>
      </Section>

      <Section id="webhooks-title" title="Related webhooks">
        <InboxTable items={a.webhooks} caption="Webhooks for this payment" empty="No webhooks for this payment" />
      </Section>

      <Section id="events-title" title="Event timeline" description="Append-only. Events that were not applied (lower precedence, duplicate, parked…) are kept and shown with the reason.">
        <EventTimeline events={a.events} />
      </Section>

      <Section id="recon-title" title="Reconciliation references">
        <ReconList items={a.reconciliation} showPayment={false} />
      </Section>

      <Section id="refunds-title" title="Refund requests">
        <RefundExecutionDisabledNotice />
        <RefundList items={a.refund_requests} meId={me.id} showPayment={false} />
        {canRefund && a.amount ? <RefundRequestForm paymentId={a.id} currency={a.amount.currency} paymentAmountMinor={a.amount.amount_minor} /> : null}
      </Section>

      {sandbox ? (
        <section aria-labelledby="sandbox-title" className="space-y-4 rounded-card border-4 border-dashed border-gold-800 bg-gold-100 p-5">
          <h2 id="sandbox-title" className="font-display text-2xl font-bold text-gold-800">
            DEVELOPMENT ONLY — SIMULATOR
          </h2>
          <p className="text-ink-900">
            Sandbox simulator state for this simulated payment. Nothing here involves a real provider or real money. Webhooks you send are signed by the simulator
            and go through the real webhook endpoint and normal processing.
          </p>
          <dl className="grid gap-3 rounded-card bg-surface p-4 sm:grid-cols-2">
            {(
              [
                ["Simulator raw status", <Code key="raw">{sandbox.raw_status ?? "—"}</Code>],
                ["Scenario", sandbox.scenario ?? "—"],
                ["Provider payment reference", <Code key="ppr">{sandbox.provider_payment_ref ?? "—"}</Code>],
                ["Our reference", <Code key="our">{sandbox.our_reference ?? "—"}</Code>],
                ["Amount", <Amount key="amt" money={sandbox.amount} />],
                ["Method", sandbox.method ? humanise(sandbox.method) : "—"],
                ["Created", <When key="c" at={sandbox.created_at} />],
                ["Updated", <When key="u" at={sandbox.updated_at} />],
              ] as const
            ).map(([label, value]) => (
              <div key={label}>
                <dt className="text-sm font-semibold text-ink-600">{label}</dt>
                <dd className="break-words text-ink-900">{value}</dd>
              </div>
            ))}
          </dl>
          <SandboxWebhookForm paymentId={a.id} />
        </section>
      ) : null}
    </AccountPage>
  );
}
