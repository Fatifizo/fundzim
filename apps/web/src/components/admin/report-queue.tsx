"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { formatDateTime } from "@/components/account/format";
import { StepUpCancelledError, useStepUp } from "@/components/auth/step-up";
import { CampaignStatusBadge } from "@/components/campaigns/status";
import { StoryText } from "@/components/campaigns/story-text";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SelectField, TextAreaField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { campaignApi, type CampaignApi } from "@/lib/campaigns/api";
import { describeCampaignError } from "@/lib/campaigns/errors";
import { humanise } from "@/lib/campaigns/labels";
import { REPORT_REASONS } from "@/lib/campaigns/report";
import type { StaffReport } from "@/lib/campaigns/types";

type Action = "dismiss" | "escalate";

export const TRIAGE_REASONS: Record<Action, string[]> = {
  dismiss: ["NO_POLICY_BREACH", "DUPLICATE_REPORT", "INSUFFICIENT_INFORMATION", "OTHER"],
  escalate: ["POSSIBLE_FRAUD", "IMPERSONATION", "PRIVACY_BREACH", "POLICY_BREACH", "OTHER"],
};

const reasonLabel = (code: string) => REPORT_REASONS.find((r) => r.value === code)?.label ?? humanise(code);

/**
 * Campaign reports queue (`campaign.review`). Dismiss or escalate needs a reason code and an internal note
 * (3–5,000 characters). Report details are plain text from the public and are rendered escaped. The reporter's
 * email is never shown: only whether one was given.
 */
export function ReportQueue({ items, api = campaignApi }: { items: StaffReport[]; api?: CampaignApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const [target, setTarget] = useState<{ report: StaffReport; action: Action } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [notice, setNotice] = useState<string | null>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  const errorRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);

  function open(report: StaffReport, action: Action) {
    setNotice(null);
    setError(null);
    setFieldErrors({});
    setTarget({ report, action });
  }

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!target) return;
    const data = new FormData(event.currentTarget);
    const reason_code = String(data.get("reason_code") ?? "");
    const note = String(data.get("note") ?? "").trim();
    const errs: Record<string, string> = {};
    if (!reason_code) errs.reason_code = "Choose a reason.";
    if ([...note].length < 3 || [...note].length > 5000) errs.note = "Write an internal note of 3 to 5,000 characters.";
    setFieldErrors(errs);
    if (Object.keys(errs).length > 0) {
      setError("Please correct the errors below.");
      requestAnimationFrame(() => errorRef.current?.focus());
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await withStepUp(() => api.triageReport(target.report.id, target.action, { reason_code, note }));
      setNotice(target.action === "dismiss" ? "Report dismissed." : "Report escalated to compliance.");
      setTarget(null);
      router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) {
        const d = describeCampaignError(err, "The decision could not be recorded.");
        setFieldErrors(d.fieldErrors);
        setError(d.message);
        requestAnimationFrame(() => errorRef.current?.focus());
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-4">
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      {items.length === 0 ? <EmptyState title="No reports here" description="Reports from the public appear here as they arrive." /> : null}
      <ul className="space-y-4" aria-label="Reports">
        {items.map((r) => (
          <li key={r.id} className="space-y-3 rounded-card border border-line bg-surface p-5">
            <div className="flex flex-wrap items-center gap-2">
              <Badge tone={r.status === "OPEN" ? "gold" : r.status === "ESCALATED" ? "info" : "neutral"}>{humanise(r.status)}</Badge>
              <span className="text-sm text-ink-600">Received {formatDateTime(r.created_at)}</span>
            </div>
            <h2 className="font-semibold break-words text-ink-900">{reasonLabel(r.reason)}</h2>
            {r.details ? <StoryText text={r.details} className="text-ink-700" /> : <p className="text-ink-600">No details given.</p>}
            <dl className="grid gap-2 text-sm sm:grid-cols-2">
              <div>
                <dt className="font-semibold text-ink-600">Campaign</dt>
                <dd className="break-words">
                  <Link href={`/admin/campaigns/review/${encodeURIComponent(r.campaign_id)}`} className="font-semibold text-brand-700 underline">
                    {r.campaign_title || "Untitled campaign"}
                  </Link>
                </dd>
              </div>
              <div>
                <dt className="font-semibold text-ink-600">Campaign status now</dt>
                <dd>
                  <CampaignStatusBadge status={r.campaign_status} audience="staff" />
                </dd>
              </div>
              <div>
                <dt className="font-semibold text-ink-600">Reporter</dt>
                <dd>{r.reporter_signed_in ? "Signed-in FundZim user" : "Anonymous visitor"}</dd>
              </div>
              <div>
                <dt className="font-semibold text-ink-600">Contact email given</dt>
                <dd>{r.has_contact ? "Yes (not shown here)" : "No"}</dd>
              </div>
              {r.triaged_at ? (
                <div className="sm:col-span-2">
                  <dt className="font-semibold text-ink-600">Decision</dt>
                  <dd className="break-words">
                    {humanise(r.triage_reason_code)} · {formatDateTime(r.triaged_at)}
                    {r.triage_note ? <StoryText text={r.triage_note} className="mt-1 text-ink-700" /> : null}
                  </dd>
                </div>
              ) : null}
            </dl>
            {r.status === "OPEN" ? (
              <div className="flex flex-wrap gap-3">
                <Button variant="outline" onClick={() => open(r, "dismiss")} aria-label={`Dismiss report about ${r.campaign_title}`}>
                  Dismiss
                </Button>
                <Button variant="primary" onClick={() => open(r, "escalate")} aria-label={`Escalate report about ${r.campaign_title}`}>
                  Escalate
                </Button>
              </div>
            ) : null}
          </li>
        ))}
      </ul>
      {target ? (
        <Dialog
          title={target.action === "dismiss" ? "Dismiss this report?" : "Escalate this report?"}
          description={
            target.action === "dismiss"
              ? "The report is closed. The campaign is not changed."
              : "A compliance case is opened for this campaign. The campaign itself is not suspended or changed by this action."
          }
          onClose={() => setTarget(null)}
        >
          <form noValidate onSubmit={onSubmit} className="space-y-5">
            <FormError ref={errorRef} message={error} />
            <SelectField label="Reason" name="reason_code" placeholderOption="Choose a reason" options={TRIAGE_REASONS[target.action].map((c) => ({ value: c, label: humanise(c) }))} error={fieldErrors.reason_code} />
            <TextAreaField label="Internal note (staff only)" name="note" required maxLength={5000} error={fieldErrors.note} />
            <div className="flex flex-wrap gap-3">
              <SubmitButton busy={busy} busyLabel="Working…">
                {target.action === "dismiss" ? "Confirm: Dismiss" : "Confirm: Escalate"}
              </SubmitButton>
              <Button variant="outline" onClick={() => setTarget(null)}>
                Cancel
              </Button>
            </div>
          </form>
        </Dialog>
      ) : null}
    </div>
  );
}
