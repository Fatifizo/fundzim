import Link from "next/link";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import { humanise, REFUND_REASON_LABELS } from "@/lib/payments/labels";
import type { ReconReference, RefundRequest } from "@/lib/payments/types";

import { Amount, attemptHref, Code, StatusBadge, When } from "./common";
import { PaymentAction, RefundActions } from "./payment-action";

/** The Stage 8 refund-execution notice (ADR-039 §4): approval is a decision only. */
export function RefundExecutionDisabledNotice() {
  return (
    <Alert tone="notice" title="Refund execution is DISABLED in Stage 8">
      Refund requests can be recorded and decided (maker-checker), but an approved refund is not sent to any provider and no money moves.
    </Alert>
  );
}

function who(p: RefundRequest["requested_by"]): string {
  if (!p) return "—";
  return p.display_name ?? `Staff member ${p.id.slice(0, 8)}`;
}

/** Refund requests with their decision controls. `meId` decides requester-only controls (the API re-checks). */
export function RefundList({ items, meId, showPayment = true }: { items: RefundRequest[]; meId: string; showPayment?: boolean }) {
  if (items.length === 0) return <EmptyState title="No refund requests" description="Refund requests for succeeded payments appear here." />;
  return (
    <ul className="space-y-4" aria-label="Refund requests">
      {items.map((r) => (
        <li key={r.id} className="space-y-3 rounded-card border border-line bg-surface p-5">
          <div className="flex flex-wrap items-center gap-2">
            <StatusBadge status={r.status} />
            <Badge tone="neutral">Execution: {r.execution}</Badge>
            <span className="font-semibold text-ink-900">
              <Amount money={r.amount} />
            </span>
          </div>
          <dl className="grid gap-2 text-sm sm:grid-cols-2">
            {showPayment ? (
              <div>
                <dt className="font-semibold text-ink-600">Payment</dt>
                <dd>
                  <Link href={attemptHref(r.payment_id)} className="text-brand-700 underline">
                    <Code>{r.payment_id}</Code>
                  </Link>
                </dd>
              </div>
            ) : null}
            <div>
              <dt className="font-semibold text-ink-600">Reason</dt>
              <dd>{REFUND_REASON_LABELS[r.reason_code] ?? humanise(r.reason_code)}</dd>
            </div>
            <div className="sm:col-span-2">
              <dt className="font-semibold text-ink-600">Explanation</dt>
              <dd className="break-words whitespace-pre-line">{r.reason_text || "—"}</dd>
            </div>
            <div>
              <dt className="font-semibold text-ink-600">Requested</dt>
              <dd>
                {who(r.requested_by)}
                {r.requested_by?.id === meId ? " (you)" : ""} · <When at={r.requested_at} />
              </dd>
            </div>
            <div>
              <dt className="font-semibold text-ink-600">Expires</dt>
              <dd>
                <When at={r.expires_at} />
              </dd>
            </div>
            {r.decided_at ? (
              <div className="sm:col-span-2">
                <dt className="font-semibold text-ink-600">Decision</dt>
                <dd className="break-words">
                  {who(r.decided_by)} · <When at={r.decided_at} />
                  {r.decision_reason ? <span className="block whitespace-pre-line">{r.decision_reason}</span> : null}
                </dd>
              </div>
            ) : null}
          </dl>
          {r.status === "APPROVED" ? <p className="text-sm font-semibold text-ink-900">Approved — execution DISABLED: this refund was not sent to any provider.</p> : null}
          <RefundActions refundId={r.id} status={r.status} isRequester={r.requested_by?.id === meId} />
        </li>
      ))}
    </ul>
  );
}

/** Reconciliation references (payment.view) with the "awaiting settlement data" action for OPEN ones. */
export function ReconList({ items, showPayment = true }: { items: ReconReference[]; showPayment?: boolean }) {
  if (items.length === 0) return <EmptyState title="No reconciliation references" description="Payments that need reconciliation appear here." />;
  return (
    <ul className="space-y-4" aria-label="Reconciliation references">
      {items.map((r) => (
        <li key={r.id} className="space-y-3 rounded-card border border-line bg-surface p-5">
          <div className="flex flex-wrap items-center gap-2">
            <StatusBadge status={r.status} />
            <span className="font-semibold text-ink-900">{humanise(r.reason)}</span>
          </div>
          <dl className="grid gap-2 text-sm sm:grid-cols-2">
            {showPayment ? (
              <div>
                <dt className="font-semibold text-ink-600">Payment</dt>
                <dd>
                  <Link href={attemptHref(r.payment_id)} className="text-brand-700 underline">
                    <Code>{r.payment_id}</Code>
                  </Link>
                </dd>
              </div>
            ) : null}
            <div>
              <dt className="font-semibold text-ink-600">Expected</dt>
              <dd>
                <Amount money={r.expected} />
              </dd>
            </div>
            <div>
              <dt className="font-semibold text-ink-600">Reported by the provider</dt>
              <dd>{r.reported ? <Amount money={r.reported} /> : "—"}</dd>
            </div>
            <div>
              <dt className="font-semibold text-ink-600">Raw provider status</dt>
              <dd>
                <Code>{r.provider_status_raw ?? "—"}</Code>
              </dd>
            </div>
            <div>
              <dt className="font-semibold text-ink-600">Provider reference</dt>
              <dd>
                <Code>{r.provider_reference ?? "—"}</Code>
              </dd>
            </div>
            <div>
              <dt className="font-semibold text-ink-600">Opened</dt>
              <dd>
                <When at={r.created_at} />
              </dd>
            </div>
            <div>
              <dt className="font-semibold text-ink-600">Evidence at</dt>
              <dd>
                <When at={r.evidence_at} />
              </dd>
            </div>
            {r.resolved_at ? (
              <div className="sm:col-span-2">
                <dt className="font-semibold text-ink-600">Resolved</dt>
                <dd className="break-words">
                  <When at={r.resolved_at} />
                  {r.resolution_note ? <span className="block whitespace-pre-line">{r.resolution_note}</span> : null}
                </dd>
              </div>
            ) : null}
          </dl>
          <PaymentAction target={{ kind: "await-settlement", referenceId: r.id }} available={r.status === "OPEN"} />
        </li>
      ))}
    </ul>
  );
}
