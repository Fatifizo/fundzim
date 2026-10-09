"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { formatDateTime } from "@/components/account/format";
import { StepUpCancelledError, useStepUp } from "@/components/auth/step-up";
import { EligibilityList } from "@/components/campaigns/eligibility-list";
import { CampaignStatusBadge, MediaStatusBadge } from "@/components/campaigns/status";
import { StoryText } from "@/components/campaigns/story-text";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SelectField, TextAreaField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { DefinitionList, CaseStatusBadge } from "@/components/verification/status";
import { isAuthRequired } from "@/lib/auth/errors";
import { loginUrl } from "@/lib/auth/safe-redirect";
import { browserNavigation } from "@/lib/browser-navigation";
import { campaignApi, type CampaignApi } from "@/lib/campaigns/api";
import { describeCampaignError } from "@/lib/campaigns/errors";
import { humanise, STAFF_ACTION_DESCRIPTIONS, STAFF_ACTION_LABELS, STAFF_REASON_CODES, staffActions, USER_MESSAGE_ACTIONS } from "@/lib/campaigns/labels";
import { formatGoal } from "@/lib/campaigns/money";
import { ownerMediaUrl } from "@/lib/campaigns/paths";
import type { CampaignMedia, EligibilityReason, StaffAction, StaffCampaignDetail } from "@/lib/campaigns/types";

export const NOTE_MIN = 3;
export const NOTE_MAX = 5000;

/** Body for a staff decision from the dialog form (contract §6: reason_code, note 3–5000, user_message?). */
export function buildDecisionBody(action: StaffAction, data: FormData): { body: Record<string, unknown>; errors: Record<string, string> } {
  const text = (k: string) => String(data.get(k) ?? "").trim();
  const errors: Record<string, string> = {};
  const reason = text("reason_code");
  const note = text("note");
  if (!reason) errors.reason_code = "Choose a reason.";
  if (note.length < NOTE_MIN || note.length > NOTE_MAX) errors.note = `Write an internal note of ${NOTE_MIN} to ${NOTE_MAX.toLocaleString("en")} characters.`;
  else if (reason === "OTHER" && note.length < 10) errors.note = "Explain the reason in at least 10 characters.";
  const body: Record<string, unknown> = { reason_code: reason, note };
  if (USER_MESSAGE_ACTIONS.has(action)) {
    const message = text("user_message");
    if (message.length < 10) errors.user_message = "Tell the owner what to change or why, in at least 10 characters.";
    body.user_message = message;
  }
  return { body, errors };
}

function DecisionDialog({
  title,
  description,
  reasons,
  withUserMessage,
  confirmLabel,
  onSubmit,
  onClose,
}: {
  title: string;
  description: string;
  reasons: readonly string[];
  withUserMessage: boolean;
  confirmLabel: string;
  onSubmit: (data: FormData) => Promise<{ errors?: Record<string, string>; message?: string; reasons?: EligibilityReason[] } | void>;
  onClose: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [eligibility, setEligibility] = useState<EligibilityReason[]>([]);
  const errorRef = useRef<HTMLDivElement>(null);
  const formRef = useRef<HTMLFormElement>(null);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    const result = await onSubmit(new FormData(event.currentTarget));
    setBusy(false);
    if (!result) return;
    setFieldErrors(result.errors ?? {});
    setError(result.message ?? null);
    setEligibility(result.reasons ?? []);
    requestAnimationFrame(() => (formRef.current?.querySelector<HTMLElement>('[aria-invalid="true"]') ?? errorRef.current)?.focus());
  }

  return (
    <Dialog title={title} description={description} onClose={onClose}>
      <form ref={formRef} noValidate onSubmit={submit} className="space-y-5">
        <FormError ref={errorRef} message={error} />
        {eligibility.length > 0 ? <EligibilityList reasons={eligibility} allowed={false} allowedText="" context={{}} fixes={false} /> : null}
        <SelectField label="Reason" name="reason_code" placeholderOption="Choose a reason" options={reasons.map((code) => ({ value: code, label: humanise(code) }))} error={fieldErrors.reason_code} />
        {withUserMessage ? (
          <TextAreaField label="Message to the owner" name="user_message" hint="Shown to the owner. Say what to change or why. No internal details." maxLength={2000} error={fieldErrors.user_message} />
        ) : null}
        <TextAreaField label="Internal note (staff only)" name="note" hint={`Required, ${NOTE_MIN}–${NOTE_MAX.toLocaleString("en")} characters. Never shown to the owner.`} required maxLength={NOTE_MAX} error={fieldErrors.note} />
        <p className="text-sm text-ink-700">You may be asked for a code from your authenticator app.</p>
        <div className="flex flex-wrap gap-3">
          <SubmitButton busy={busy} busyLabel="Working…">
            {confirmLabel}
          </SubmitButton>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

/**
 * Staff review of one campaign. Every action opens a dialog with a reason code and a required note; the
 * API asks for step-up (STEP_UP_REQUIRED) on decisions, handled by StepUpProvider. Second approval is never
 * offered to the first approver. The compliance restriction indicator is generic: no reason is shown.
 */
export function CampaignReviewView({ detail, meId, api = campaignApi }: { detail: StaffCampaignDetail; meId: string; api?: CampaignApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const [action, setAction] = useState<StaffAction | null>(null);
  const [removing, setRemoving] = useState<CampaignMedia | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  const c = detail.campaign;
  const r = detail.review;
  const actions = staffActions({ status: String(c.status), review: r, meId }, detail.allowed_actions);

  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);

  /** Runs a staff call with step-up; returns form feedback on failure, undefined on success. */
  async function run(call: () => Promise<string>) {
    try {
      const done = await withStepUp(call);
      setAction(null);
      setRemoving(null);
      setNotice(done);
      router.refresh();
      return undefined;
    } catch (error) {
      if (error instanceof StepUpCancelledError) return { message: "Confirmation was cancelled. Nothing was changed." };
      if (isAuthRequired(error)) {
        browserNavigation.assign(loginUrl(browserNavigation.currentPath()));
        return undefined;
      }
      const d = describeCampaignError(error, "This action could not be completed. Please try again.");
      return { message: d.message, errors: d.fieldErrors, reasons: d.reasons };
    }
  }

  return (
    <div className="space-y-6">
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      {detail.restricted ? (
        <Alert tone="notice" title="Compliance restriction">
          A compliance restriction applies to a party linked to this campaign. Details are held by compliance; decisions may be refused.
        </Alert>
      ) : null}
      <Card as="section" aria-labelledby="summary-title">
        <h2 id="summary-title" className="font-display text-xl font-semibold text-ink-900">
          Campaign
        </h2>
        <div className="mt-4">
          <DefinitionList
            items={[
              { term: "Status", value: <CampaignStatusBadge status={String(c.status)} audience="staff" /> },
              { term: "Owner", value: c.owner_display_name ?? "—" },
              { term: "Category", value: humanise(c.category) },
              { term: "Goal", value: formatGoal(c.goal).text },
              { term: "Risk tier", value: r.risk_tier ? humanise(r.risk_tier) : "—" },
              { term: "Assigned to", value: r.assigned_to ? `${r.assigned_to.display_name}${r.assigned_to.id === meId ? " (you)" : ""}` : "Unassigned" },
              { term: "Submitted", value: formatDateTime(c.submitted_at) },
              { term: "Resubmissions", value: String(c.resubmission_count) },
            ]}
          />
        </div>
        <p className="mt-3 flex flex-wrap gap-2">
          {r.requires_second_approval ? <Badge tone="info">Four eyes: two different approvers needed</Badge> : null}
          {r.escalated ? <Badge tone="gold">Escalated to compliance</Badge> : null}
        </p>
        {r.pending_outcome ? (
          <Alert tone="info" title="Waiting for a second approval" className="mt-4">
            {r.pending_decided_by === meId
              ? "You gave the first approval. A different reviewer must give the second approval."
              : `A first decision (${humanise(r.pending_outcome)}) was recorded by another reviewer. High-risk campaigns need two different approvers.`}
          </Alert>
        ) : null}
      </Card>

      <Card as="section" aria-labelledby="actions-title">
        <h2 id="actions-title" className="font-display text-xl font-semibold text-ink-900">
          Actions
        </h2>
        {actions.length === 0 ? (
          <p className="mt-2 text-ink-700">No actions are available to you for this campaign right now.</p>
        ) : (
          <div className="mt-4 flex flex-wrap gap-3">
            {actions.map((a) => (
              <Button key={a} variant={a === "approve" || a === "second-approval" || a === "publish" ? "primary" : a === "assign" || a === "start-review" ? "secondary" : "outline"} onClick={() => { setNotice(null); setAction(a); }}>
                {STAFF_ACTION_LABELS[a]}
              </Button>
            ))}
          </div>
        )}
        <p className="mt-3 text-sm text-ink-600">You cannot decide a campaign that involves you, your organisation or your linked personal account. The system enforces this.</p>
      </Card>

      {detail.eligibility.length > 0 ? (
        <Card as="section" aria-labelledby="eligibility-title">
          <h2 id="eligibility-title" className="font-display text-xl font-semibold text-ink-900">
            Eligibility
          </h2>
          <div className="mt-4 space-y-5">
            {detail.eligibility.map((e) => (
              <div key={e.action} className="space-y-2">
                <h3 className="font-semibold text-ink-900">{humanise(e.action)}</h3>
                <EligibilityList reasons={e.reasons} allowed={e.allowed} allowedText="All automated checks pass." context={{}} fixes={false} />
              </div>
            ))}
          </div>
        </Card>
      ) : null}

      <Card as="section" aria-labelledby="beneficiary-title">
        <h2 id="beneficiary-title" className="font-display text-xl font-semibold text-ink-900">
          Beneficiary
        </h2>
        {detail.beneficiary ? (
          <div className="mt-4">
            <DefinitionList
              items={[
                { term: "Name", value: detail.beneficiary.display_name ?? "—" },
                { term: "Type", value: humanise(detail.beneficiary.beneficiary_type) },
                { term: "Verification", value: <CaseStatusBadge status={detail.beneficiary.verification_status} audience="reviewer" /> },
                { term: "Public disclosure", value: c.beneficiary?.disclosure === "DISPLAY_NAME" ? "Display name shown" : "Details private" },
              ]}
            />
          </div>
        ) : (
          <p className="mt-2 text-ink-700">No beneficiary linked.</p>
        )}
      </Card>

      <Card as="section" aria-labelledby="content-title">
        <h2 id="content-title" className="font-display text-xl font-semibold text-ink-900">
          Content under review
        </h2>
        <h3 className="mt-4 font-semibold break-words text-ink-900">{c.title}</h3>
        <p className="mt-1 break-words text-ink-700">{c.summary}</p>
        <StoryText text={c.story} className="mt-4 text-ink-700" />
      </Card>

      <Card as="section" aria-labelledby="media-title">
        <h2 id="media-title" className="font-display text-xl font-semibold text-ink-900">
          Photos
        </h2>
        {detail.media.length === 0 ? <p className="mt-2 text-ink-700">No photos.</p> : null}
        <ul className="mt-3 divide-y divide-line">
          {detail.media.map((m) => (
            <li key={m.id} className="flex flex-wrap items-center justify-between gap-3 py-3">
              <span className="flex items-start gap-3">
                {m.status === "APPROVED" && ownerMediaUrl(c.id, m.id) ? (
                  // eslint-disable-next-line @next/next/no-img-element -- same-origin processed image; next/image emits inline styles the CSP blocks
                  <img src={ownerMediaUrl(c.id, m.id)!} alt={m.alt_text} width={160} height={90} loading="lazy" className="aspect-video w-32 shrink-0 rounded-lg bg-surface-muted object-cover" />
                ) : null}
                <span className="space-y-1">
                <span className="flex flex-wrap items-center gap-2">
                  <span className="font-semibold text-ink-900">{m.kind === "COVER" ? "Cover" : "Gallery"}</span>
                  <MediaStatusBadge status={m.status} />
                </span>
                <span className="block text-sm break-words text-ink-700">{m.alt_text || "No description"}</span>
                </span>
              </span>
              {m.status !== "REMOVED" ? (
                <Button variant="outline" onClick={() => { setNotice(null); setRemoving(m); }} aria-label={`Remove photo: ${m.alt_text || m.id}`}>
                  Remove
                </Button>
              ) : null}
            </li>
          ))}
        </ul>
      </Card>

      <Card as="section" aria-labelledby="history-title">
        <h2 id="history-title" className="font-display text-xl font-semibold text-ink-900">
          Review history
        </h2>
        {detail.history.length === 0 ? (
          <p className="mt-2 text-ink-700">No events yet.</p>
        ) : (
          <ol className="mt-3 divide-y divide-line">
            {detail.history.map((h) => (
              <li key={h.id} className="py-2 text-ink-700">
                <p className="flex flex-wrap justify-between gap-2">
                  <span className="font-semibold text-ink-900">{h.from_status || h.to_status ? `${humanise(h.from_status ?? "new")} → ${humanise(h.to_status)}` : humanise(h.action)}</span>
                  <span className="text-sm text-ink-600">{formatDateTime(h.occurred_at)}</span>
                </p>
                <p className="text-sm">
                  {h.actor?.display_name ?? humanise(h.actor?.type ?? "system")}
                  {h.reason_code ? ` · ${humanise(h.reason_code)}` : ""}
                </p>
                {h.note ? <p className="mt-1 text-sm whitespace-pre-line break-words">{h.note}</p> : null}
              </li>
            ))}
          </ol>
        )}
      </Card>

      {action ? (
        <DecisionDialog
          title={STAFF_ACTION_LABELS[action]}
          description={STAFF_ACTION_DESCRIPTIONS[action]}
          reasons={STAFF_REASON_CODES[action]}
          withUserMessage={USER_MESSAGE_ACTIONS.has(action)}
          confirmLabel={`Confirm: ${STAFF_ACTION_LABELS[action]}`}
          onClose={() => setAction(null)}
          onSubmit={async (data) => {
            const { body, errors } = buildDecisionBody(action, data);
            if (Object.keys(errors).length > 0) return { errors, message: "Please correct the errors below." };
            return run(async () => {
              const result = await api.staffAction(c.id, action, body);
              // Action responses are {campaign_id, status, awaiting_second_approval}.
              const data = (result.data ?? {}) as { status?: string; awaiting_second_approval?: boolean };
              const pending = action === "approve" && (data.awaiting_second_approval === true || result.status === 202);
              return pending ? "First approval recorded. A second approval from a different reviewer is needed." : `${STAFF_ACTION_LABELS[action]}: done.`;
            });
          }}
        />
      ) : null}
      {removing ? (
        <DecisionDialog
          title="Remove this photo?"
          description="The photo is removed from the campaign and the public page. The owner is told it was removed."
          reasons={["INAPPROPRIATE_CONTENT", "PRIVACY", "COPYRIGHT", "MISLEADING", "OTHER"]}
          withUserMessage={false}
          confirmLabel="Remove photo"
          onClose={() => setRemoving(null)}
          onSubmit={async (data) => {
            const { body, errors } = buildDecisionBody("cancel", data);
            if (Object.keys(errors).length > 0) return { errors, message: "Please correct the errors below." };
            return run(async () => {
              await api.removeMedia(c.id, removing.id, body as { reason_code: string; note: string });
              return "Photo removed.";
            });
          }}
        />
      ) : null}
    </div>
  );
}
