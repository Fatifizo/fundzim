import type { ReactNode } from "react";

import { formatDateTime } from "@/components/account/format";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { caseStatusLabel, humanise, type ProgressStep } from "@/lib/verification/labels";
import type { Decision, InformationRequest } from "@/lib/verification/types";

/** Case status as a text badge (meaning carried by the text, never by colour alone). */
export function CaseStatusBadge({ status, audience = "user" }: { status: string | null | undefined; audience?: "user" | "reviewer" }) {
  const { label, tone } = caseStatusLabel(status, audience);
  return <Badge tone={tone}>{label}</Badge>;
}

export function InformationRequests({ requests, id, heading = "Requests for more information" }: { requests: InformationRequest[]; id?: string; heading?: string }) {
  if (requests.length === 0) return null;
  const open = requests.filter((r) => !r.responded_at);
  return (
    <section id={id} aria-labelledby={id ? `${id}-title` : undefined} tabIndex={-1} className="space-y-3 focus:outline-none">
      <h2 id={id ? `${id}-title` : undefined} className="font-display text-xl font-semibold text-ink-900">
        {heading}
      </h2>
      <ul className="space-y-3">
        {requests.map((request) => (
          <li key={request.id} className="rounded-xl border border-line bg-surface p-4">
            <p className="flex flex-wrap items-center gap-2 text-sm text-ink-600">
              <span>Requested {formatDateTime(request.requested_at)}</span>
              {request.responded_at ? <Badge tone="brand">Answered {formatDateTime(request.responded_at)}</Badge> : <Badge tone="gold">Waiting for you</Badge>}
            </p>
            <p className="mt-2 whitespace-pre-line break-words text-ink-900">{request.message}</p>
            {request.items.length > 0 ? (
              <ul className="mt-2 list-disc space-y-1 pl-6 text-ink-700">
                {request.items.map((item) => (
                  <li key={item}>{humanise(item)}</li>
                ))}
              </ul>
            ) : null}
          </li>
        ))}
      </ul>
      {open.length > 0 ? (
        <p className="text-ink-700">
          To respond, update your details or upload the documents asked for, then submit again. Your answer is sent when you submit.
        </p>
      ) : null}
    </section>
  );
}

export function DecisionNotice({ decision, status }: { decision: Decision | null | undefined; status: string }) {
  if (!decision) return null;
  const approved = status === "APPROVED" || decision.outcome === "APPROVED";
  return (
    <Alert tone={approved ? "info" : "notice"} title={approved ? "Verification decision: verified" : "Verification decision: not approved"}>
      <p>Decided {formatDateTime(decision.decided_at)}.</p>
      {decision.message ? <p className="mt-1 whitespace-pre-line break-words">{decision.message}</p> : null}
      {!approved ? <p className="mt-1">If you think this is a mistake, you can start a new verification with corrected details, or contact us.</p> : null}
    </Alert>
  );
}

/** Ordered progress list; each step states its status in text. */
export function ProgressList({ steps, label }: { steps: ProgressStep[]; label: string }) {
  const text = { done: "Done", current: "Next", todo: "Not yet" } as const;
  return (
    <ol aria-label={label} className="grid gap-2 sm:grid-cols-5">
      {steps.map((step, index) => (
        <li
          key={step.key}
          aria-current={step.state === "current" ? "step" : undefined}
          className={
            "rounded-xl border-2 p-3 " +
            (step.state === "done" ? "border-brand-700 bg-brand-50" : step.state === "current" ? "border-gold-500 bg-gold-100" : "border-line bg-surface")
          }
        >
          <span className="block text-sm font-semibold text-ink-600">
            Step {index + 1} · {text[step.state]}
          </span>
          <span className="block font-semibold text-ink-900">{step.label}</span>
        </li>
      ))}
    </ol>
  );
}

export function DefinitionList({ items }: { items: Array<{ term: string; value: ReactNode }> }) {
  return (
    <dl className="grid gap-3 text-ink-700 sm:grid-cols-2">
      {items.map((item) => (
        <div key={item.term}>
          <dt className="text-sm font-semibold text-ink-600">{item.term}</dt>
          <dd className="break-words">{item.value ?? "—"}</dd>
        </div>
      ))}
    </dl>
  );
}

/** Always-visible statement that payouts do not exist yet (Stage 5: `eligible_for_payout` is always false). */
export function PayoutsUnavailableNotice() {
  return (
    <Alert tone="notice" title="Payouts are not available yet">
      FundZim cannot send money to any account yet. Adding and verifying an account now only prepares it; no payout can be requested or made.
    </Alert>
  );
}
