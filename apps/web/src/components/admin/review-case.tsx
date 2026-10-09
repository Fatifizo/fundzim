"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { formatDateTime } from "@/components/account/format";
import { useStepUp } from "@/components/auth/step-up";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SelectField, TextAreaField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { DocumentManager } from "@/components/verification/document-manager";
import { CaseStatusBadge, DefinitionList, InformationRequests } from "@/components/verification/status";
import { useVerificationFeedback } from "@/components/verification/use-verification-feedback";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { verificationApi, type VerificationApi } from "@/lib/verification/api";
import { formatOwnership } from "@/lib/verification/basis-points";
import { countryName } from "@/lib/verification/countries";
import { deriveReviewActions, humanise, ID_DOCUMENT_TYPE_LABELS, PERSON_ROLE_LABELS, REASON_CODES, REVIEW_ACTION_LABELS } from "@/lib/verification/labels";
import type { ReviewAction, ReviewCase, SubjectType } from "@/lib/verification/types";

/** Actions that record a decision and therefore need a fresh step-up (contract §7.3). */
const STEP_UP_ACTIONS: ReadonlySet<ReviewAction> = new Set(["approve", "second-approval", "reject", "suspend", "reinstate", "reopen", "revoke"]);
const REVEAL_SECONDS = 60;

export const ACTION_DESCRIPTIONS: Record<ReviewAction, string> = {
  assign: "The case will be assigned to you. Only the assigned reviewer can review and decide it.",
  "start-review": "The case moves to Under review. The subject can no longer withdraw it.",
  "request-info": "The subject is asked for more information and can edit and resubmit. Your message is shown to them.",
  approve: "Record an approval. Cases that need four eyes then wait for a second reviewer.",
  "second-approval": "Give the second, independent approval. You must not be the first approver.",
  reject: "Record a rejection. The subject sees the message for them, never your internal note.",
  escalate: "Send the case to compliance. It stays escalated until compliance returns or decides it.",
  suspend: "Suspend an approved verification. The subject loses the features it unlocked until reinstated.",
  reinstate: "Reinstate a suspended verification as approved.",
  reopen: "Reopen a suspended verification for review.",
  return: "Return an escalated case to review (for example after compliance has looked at it).",
  revoke: "Revoke the verification permanently. A new verification will be needed.",
};

/** Actions whose request carries a required reviewer note. */
export const NOTE_REQUIRED: ReadonlySet<ReviewAction> = new Set(["approve", "second-approval", "reject", "escalate", "suspend", "reinstate", "reopen", "return", "revoke"]);

/** Body for each reviewer action from the dialog's form; returns field errors for missing input. */
export function buildActionBody(action: ReviewAction, data: FormData, type?: string): { body: Record<string, unknown>; errors: Record<string, string> } {
  const text = (key: string) => String(data.get(key) ?? "").trim();
  const errors: Record<string, string> = {};
  const body: Record<string, unknown> = {};
  const reason = text("reason_code");
  const note = text("note");
  if (REASON_CODES[action]) {
    if (!reason) errors.reason_code = "Choose a reason.";
    else body.reason_code = reason;
    if (reason === "OTHER" && note.length < 10) errors.note = "Explain the reason in at least 10 characters.";
  }
  if (NOTE_REQUIRED.has(action)) {
    // Decisions require a reviewer note (API: 3–5000 characters, else 422 VALIDATION_FAILED note/INVALID_LENGTH).
    if (!errors.note && (note.length < 3 || note.length > 5000)) errors.note = "Write an internal note of at least 3 characters explaining the decision.";
    body.note = note;
  }
  if (action === "reject") {
    const message = text("user_message");
    if (message.length < 10) errors.user_message = "Tell the person what was wrong and what they can do, in at least 10 characters.";
    body.user_message = message;
  }
  if (action === "request-info") {
    const message = text("message");
    if (message.length < 10) errors.message = "Write the request in at least 10 characters.";
    body.message = message;
    body.items = text("items")
      .split(/\n|,/)
      .map((item) => item.trim())
      .filter(Boolean);
  }
  if (type === "PAYOUT_DESTINATION" && (action === "approve" || action === "reject")) body.name_match = data.get("name_match") === "yes";
  return { body, errors };
}

function ActionDialog({ action, caseId, type, api, onDone, onClose }: { action: ReviewAction; caseId: string; type: string; api: VerificationApi; onDone: (message: string) => void; onClose: () => void }) {
  const withStepUp = useStepUp();
  const [busy, setBusy] = useState(false);
  const { formError, fieldErrors, errorRef, formRef, clear, fail, failWith } = useVerificationFeedback();
  const reasons = REASON_CODES[action];

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const { body, errors } = buildActionBody(action, new FormData(event.currentTarget), type);
    if (Object.keys(errors).length > 0) return fail("Please correct the errors below.", errors);
    clear();
    setBusy(true);
    try {
      const call = () => api.reviewAction(caseId, action, body);
      const result = STEP_UP_ACTIONS.has(action) ? await withStepUp(call) : await call();
      const status = (result.data as { status?: string } | undefined)?.status;
      onDone(
        result.status === 202 || status === "AWAITING_SECOND_APPROVAL"
          ? "First approval recorded. The case now needs a second approval from a different reviewer."
          : `${REVIEW_ACTION_LABELS[action]}: done.`,
      );
    } catch (error) {
      failWith(error, "This action could not be completed. Please try again.");
      setBusy(false);
    }
  }

  return (
    <Dialog title={REVIEW_ACTION_LABELS[action]} description={ACTION_DESCRIPTIONS[action]} onClose={onClose}>
      <form ref={formRef} noValidate onSubmit={onSubmit} className="space-y-5">
        <FormError ref={errorRef} message={formError} />
        {reasons ? (
          <SelectField label="Reason" name="reason_code" placeholderOption="Choose a reason" options={reasons.map((code) => ({ value: code, label: humanise(code) }))} error={fieldErrors.reason_code} />
        ) : null}
        {action === "request-info" ? (
          <>
            <TextAreaField label="Message to the person" name="message" hint="Shown to them. Say exactly what you need and why." error={fieldErrors.message} maxLength={2000} />
            <TextAreaField label="Items requested (optional)" name="items" rows={3} hint="One per line, e.g. PROOF_OF_ADDRESS." maxLength={1000} />
          </>
        ) : null}
        {action === "reject" ? (
          <TextAreaField label="Message to the person" name="user_message" hint="Shown to them. Explain what was wrong and what they can do next. No internal details." error={fieldErrors.user_message} maxLength={2000} />
        ) : null}
        {type === "PAYOUT_DESTINATION" && (action === "approve" || action === "reject") ? (
          <label className="flex min-h-11 items-center gap-3 text-ink-900">
            <input type="checkbox" name="name_match" value="yes" className="size-5 accent-brand-700" />
            The account holder name matches the evidence
          </label>
        ) : null}
        {NOTE_REQUIRED.has(action) ? (
          <TextAreaField label="Internal note (reviewers only)" name="note" hint="Required. Reviewers only; never shown to the person." required error={fieldErrors.note} maxLength={2000} />
        ) : null}
        {STEP_UP_ACTIONS.has(action) ? <p className="text-sm text-ink-700">You may be asked for a code from your authenticator app.</p> : null}
        <div className="flex flex-wrap gap-3">
          <SubmitButton busy={busy} busyLabel="Working…">
            {`Confirm: ${REVIEW_ACTION_LABELS[action]}`}
          </SubmitButton>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

function RevealIdentityNumber({ caseId, api }: { caseId: string; api: VerificationApi }) {
  const withStepUp = useStepUp();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [revealed, setRevealed] = useState<string | null>(null);
  const [secondsLeft, setSecondsLeft] = useState(0);
  const { formError, fieldErrors, errorRef, formRef, clear, fail, failWith } = useVerificationFeedback();

  useEffect(() => {
    if (!revealed) return;
    const timer = setInterval(() => {
      setSecondsLeft((s) => {
        if (s <= 1) {
          setRevealed(null);
          return 0;
        }
        return s - 1;
      });
    }, 1000);
    return () => clearInterval(timer);
  }, [revealed]);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const justification = String(new FormData(event.currentTarget).get("justification") ?? "").trim();
    if (justification.length < 10) return fail("Please correct the errors below.", { justification: "Give a justification of at least 10 characters. It is recorded in the audit log." });
    clear();
    setBusy(true);
    try {
      const result = await withStepUp(() => api.revealIdentityNumber(caseId, justification));
      setRevealed(result.id_document_number);
      setSecondsLeft(REVEAL_SECONDS);
      setOpen(false);
    } catch (error) {
      failWith(error, "The number could not be revealed.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mt-4">
      {revealed ? (
        <div role="status" className="rounded-xl border border-gold-500/50 bg-gold-100 p-4">
          <p className="font-semibold text-gold-800">Full ID number (hidden again in {secondsLeft} s)</p>
          <p className="mt-1 font-mono text-lg tracking-wider break-all text-ink-900">{revealed}</p>
          <Button variant="outline" className="mt-2" onClick={() => setRevealed(null)}>
            Hide now
          </Button>
        </div>
      ) : (
        <Button variant="outline" onClick={() => setOpen(true)}>
          Reveal full ID number
        </Button>
      )}
      {open ? (
        <Dialog title="Reveal the full ID number?" description="Only reveal it when you need it to decide this case. Your justification and the reveal are recorded in the audit log." onClose={() => { setOpen(false); clear(); }}>
          <form ref={formRef} noValidate onSubmit={onSubmit} className="space-y-5">
            <FormError ref={errorRef} message={formError} />
            <TextAreaField label="Justification" name="justification" error={fieldErrors.justification} maxLength={500} />
            <div className="flex flex-wrap gap-3">
              <SubmitButton busy={busy} busyLabel="Checking…">
                Reveal
              </SubmitButton>
              <Button variant="outline" onClick={() => setOpen(false)}>
                Cancel
              </Button>
            </div>
          </form>
        </Dialog>
      ) : null}
    </div>
  );
}

const SUBJECT_DOC_TYPE: Record<string, SubjectType> = {
  KYC: "KYC_CASE",
  KYB: "KYB_CASE",
  BENEFICIARY: "BENEFICIARY",
  PAYOUT_DESTINATION: "PAYOUT_DESTINATION",
};

export function ReviewCaseView({ reviewCase, meId, api = verificationApi }: { reviewCase: ReviewCase; meId: string; api?: VerificationApi }) {
  const router = useRouter();
  const [action, setAction] = useState<ReviewAction | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  const c = reviewCase;
  const assignedToMe = c.assigned_to?.id === meId;
  const actions = c.allowed_actions ?? deriveReviewActions({
      type: c.type,
      status: c.status,
      assignedToMe,
      hasPendingApproval: !!c.pending_approval,
      pendingApprovalByMe: c.pending_approval?.approved_by.id === meId,
    });

  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);

  const identity = c.identity;
  return (
    <div className="space-y-6">
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      <Card as="section" aria-labelledby="case-summary-title">
        <h2 id="case-summary-title" className="font-display text-xl font-semibold text-ink-900">
          Case
        </h2>
        <div className="mt-4">
          <DefinitionList
            items={[
              { term: "Status", value: <CaseStatusBadge status={c.status} audience="reviewer" /> },
              { term: "Type", value: humanise(c.type) },
              { term: "Subject", value: c.subject.display_name ?? `${humanise(c.subject.type)} ${c.subject.id}` },
              { term: "Risk level", value: c.risk_level ? humanise(c.risk_level) : "—" },
              { term: "Assigned to", value: c.assigned_to ? `${c.assigned_to.display_name}${assignedToMe ? " (you)" : ""}` : "Unassigned" },
              { term: "Submitted", value: formatDateTime(c.submitted_at) },
            ]}
          />
        </div>
        {c.requires_second_approval ? <p className="mt-3"><Badge tone="info">Four-eyes: two different reviewers must approve</Badge></p> : null}
        {c.pending_approval ? (
          <Alert tone="info" title="Waiting for a second approval" className="mt-4">
            {c.pending_approval.approved_by.id === meId
              ? "You gave the first approval. A different reviewer must give the second approval."
              : `First approval${c.pending_approval.outcome ? ` (${humanise(c.pending_approval.outcome)})` : ""} by ${c.pending_approval.approved_by.display_name}${c.pending_approval.approved_at ? ` on ${formatDateTime(c.pending_approval.approved_at)}` : ""}.`}
          </Alert>
        ) : null}
      </Card>

      <Card as="section" aria-labelledby="actions-title">
        <h2 id="actions-title" className="font-display text-xl font-semibold text-ink-900">
          Actions
        </h2>
        {actions.length === 0 ? (
          <p className="mt-2 text-ink-700">No actions are available to you for this case right now.</p>
        ) : (
          <div className="mt-4 flex flex-wrap gap-3">
            {actions.map((a) => (
              <Button key={a} variant={a === "approve" || a === "second-approval" ? "primary" : a === "assign" || a === "start-review" ? "secondary" : "outline"} onClick={() => { setNotice(null); setAction(a); }}>
                {REVIEW_ACTION_LABELS[a]}
              </Button>
            ))}
          </div>
        )}
        <p className="mt-3 text-sm text-ink-600">You cannot decide a case that involves you, your organisation or a beneficiary you own. The system enforces this.</p>
      </Card>

      <Card as="section" aria-labelledby="subject-title">
        <h2 id="subject-title" className="font-display text-xl font-semibold text-ink-900">
          Subject details (masked)
        </h2>
        <div className="mt-4">
          {c.subject_summary && c.subject_summary.length > 0 ? (
            <DefinitionList items={c.subject_summary.map((row) => ({ term: row.label, value: row.value }))} />
          ) : identity ? (
            <DefinitionList
              items={[
                { term: "Name", value: [identity.legal_first_name, identity.legal_last_name].filter(Boolean).join(" ") || "—" },
                { term: "Date of birth", value: identity.date_of_birth ?? "—" },
                { term: "Nationality", value: countryName(identity.nationality) },
                { term: "Residence", value: countryName(identity.country_of_residence) },
                { term: "ID document", value: identity.id_document_type ? ID_DOCUMENT_TYPE_LABELS[identity.id_document_type] ?? identity.id_document_type : "—" },
                { term: "ID number", value: identity.id_document_number_masked ?? "—" },
                { term: "ID expiry", value: identity.id_document_expiry ?? "—" },
              ]}
            />
          ) : c.details ? (
            <DefinitionList
              items={[
                { term: "Registered name", value: c.details.registered_name ?? "—" },
                { term: "Registration number", value: c.details.registration_number ?? "—" },
                { term: "Registry", value: c.details.registry ?? "—" },
                { term: "Country", value: countryName(c.details.country_of_registration) },
              ]}
            />
          ) : (
            <p className="text-ink-700">No subject details.</p>
          )}
        </div>
        {c.type === "KYC" && identity?.id_document_number_masked ? <RevealIdentityNumber caseId={c.id} api={api} /> : null}
        {c.persons && c.persons.length > 0 ? (
          <div className="mt-5">
            <h3 className="font-semibold text-ink-900">People</h3>
            <ul className="mt-2 space-y-1 text-ink-700">
              {c.persons.map((p) => (
                <li key={p.id}>
                  {p.full_name} — {p.roles.map((r) => PERSON_ROLE_LABELS[r] ?? r).join(", ")}
                  {p.ownership_bp !== null ? ` — ${formatOwnership(p.ownership_bp)}` : ""}
                  {p.id_document_number_masked ? ` — ID ${p.id_document_number_masked}` : ""}
                </li>
              ))}
            </ul>
          </div>
        ) : null}
      </Card>

      {c.risk ? (
        <Card as="section" aria-labelledby="risk-title">
          <h2 id="risk-title" className="font-display text-xl font-semibold text-ink-900">
            Risk signals
          </h2>
          <p className="mt-2 text-ink-700">Internal categories to guide review, not conclusions.</p>
          {c.risk.signals.length === 0 ? (
            <p className="mt-2 text-ink-700">No signals.</p>
          ) : (
            <ul className="mt-2 list-disc space-y-1 pl-6 text-ink-700">
              {c.risk.signals.map((s, i) => (
                <li key={`${s.code}-${i}`}>
                  {humanise(s.code)}
                  {s.description ? ` — ${s.description}` : ""}
                </li>
              ))}
            </ul>
          )}
        </Card>
      ) : null}

      <Card>
        <DocumentManager
          key={c.documents.map((d) => `${d.id}:${d.status}`).join(",")}
          subjectType={SUBJECT_DOC_TYPE[c.type] ?? "KYC_CASE"}
          subjectId={c.subject.id}
          initialDocuments={c.documents}
          requirements={c.requirements}
          editable={false}
          heading="Documents"
          description="Viewing a document needs a fresh authenticator code and is recorded in the audit log. Files download; open them only on a work device."
          api={api}
        />
      </Card>

      {c.information_requests && c.information_requests.length > 0 ? (
        <Card>
          <InformationRequests requests={c.information_requests} heading="Information requested" />
        </Card>
      ) : null}

      <Card as="section" aria-labelledby="history-title">
        <h2 id="history-title" className="font-display text-xl font-semibold text-ink-900">
          Audit history
        </h2>
        {c.history.length === 0 ? (
          <p className="mt-2 text-ink-700">No events yet.</p>
        ) : (
          <ol className="mt-3 divide-y divide-line">
            {c.history.map((h) => (
              <li key={h.id} className="py-2 text-ink-700">
                <p className="flex flex-wrap justify-between gap-2">
                  <span className="font-semibold text-ink-900">{humanise(h.action)}</span>
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
        <ActionDialog
          action={action}
          caseId={c.id}
          type={c.type}
          api={api}
          onClose={() => setAction(null)}
          onDone={(message) => {
            setAction(null);
            setNotice(message);
            router.refresh();
          }}
        />
      ) : null}
    </div>
  );
}
