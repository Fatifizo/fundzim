"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { formatDateTime } from "@/components/account/format";
import { useStepUp } from "@/components/auth/step-up";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SelectField, TextAreaField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { DefinitionList } from "@/components/verification/status";
import { useVerificationFeedback } from "@/components/verification/use-verification-feedback";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { verificationApi, type VerificationApi } from "@/lib/verification/api";
import { COMPLIANCE_ACTION_LABELS, COMPLIANCE_DECISIONS, COMPLIANCE_REASON_CODES, complianceStatusLabel, deriveComplianceActions, humanise } from "@/lib/verification/labels";
import type { ComplianceAction, ComplianceCase } from "@/lib/verification/types";

/** Step-up for resolve / approve-resolution / close / reopen (contract §7.4). */
const STEP_UP: ReadonlySet<ComplianceAction> = new Set(["resolve", "approve-resolution", "close", "reopen"]);

/**
 * Request bodies per compliance action (internal/compliance http.go): request-info, escalate, resolve, close
 * and reopen carry an UPPER_SNAKE `reason_code` plus a `note`; resolve adds `decision`; escalate may change
 * `severity`; approve-resolution takes a `note`. Bodies are decoded strictly, so no other fields are sent.
 */
export function buildComplianceBody(action: ComplianceAction, data: FormData): { body: Record<string, unknown>; errors: Record<string, string> } {
  const text = (key: string) => String(data.get(key) ?? "").trim();
  const errors: Record<string, string> = {};
  const body: Record<string, unknown> = {};
  const note = text("note");
  if (COMPLIANCE_REASON_CODES[action]) {
    const reason = text("reason_code");
    if (!reason) errors.reason_code = "Choose a reason.";
    body.reason_code = reason;
    const minNote = action === "resolve" || action === "reopen" || reason === "OTHER" ? 10 : 0;
    if (note.length < minNote) errors.note = action === "resolve" ? "Record the basis for the resolution in at least 10 characters." : "Explain in at least 10 characters.";
    body.note = note;
  }
  if (action === "resolve") {
    const decision = text("decision");
    if (!decision) errors.decision = "Choose a resolution.";
    body.decision = decision;
  }
  if (action === "escalate") {
    const severity = text("severity");
    if (severity) body.severity = severity;
  }
  if (action === "approve-resolution") body.note = note;
  return { body, errors };
}

function ActionDialog({ action, caseId, api, onDone, onClose }: { action: ComplianceAction; caseId: string; api: VerificationApi; onDone: () => void; onClose: () => void }) {
  const withStepUp = useStepUp();
  const [busy, setBusy] = useState(false);
  const { formError, fieldErrors, errorRef, formRef, clear, fail, failWith } = useVerificationFeedback();

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const { body, errors } = buildComplianceBody(action, new FormData(event.currentTarget));
    if (Object.keys(errors).length > 0) return fail("Please correct the errors below.", errors);
    clear();
    setBusy(true);
    try {
      const call = () => api.complianceAction(caseId, action, body);
      await (STEP_UP.has(action) ? withStepUp(call) : call());
      onDone();
    } catch (error) {
      failWith(error, "This action could not be completed. Please try again.");
      setBusy(false);
    }
  }

  const reasons = COMPLIANCE_REASON_CODES[action];
  return (
    <Dialog title={COMPLIANCE_ACTION_LABELS[action]} description={action === "approve-resolution" ? "Approving makes the proposed resolution final. You must not be the person who proposed it." : undefined} onClose={onClose}>
      <form ref={formRef} noValidate onSubmit={onSubmit} className="space-y-5">
        <FormError ref={errorRef} message={formError} />
        {reasons ? <SelectField label="Reason" name="reason_code" placeholderOption="Choose a reason" options={reasons.map((r) => ({ value: r, label: humanise(r) }))} error={fieldErrors.reason_code} /> : null}
        {action === "resolve" ? <SelectField label="Resolution" name="decision" placeholderOption="Choose a resolution" options={COMPLIANCE_DECISIONS.map((d) => ({ value: d, label: humanise(d) }))} error={fieldErrors.decision} /> : null}
        {action === "escalate" ? (
          <SelectField label="Change severity (optional)" name="severity" placeholderOption="Keep the current severity" options={[{ value: "S1", label: "S1 (highest)" }, { value: "S2", label: "S2" }, { value: "S3", label: "S3 (lowest)" }]} />
        ) : null}
        {action !== "assign" && action !== "start" ? (
          <TextAreaField label={action === "request-info" ? "What information is needed (note)" : "Note"} name="note" error={fieldErrors.note} maxLength={4000} />
        ) : null}
        {STEP_UP.has(action) ? <p className="text-sm text-ink-700">You may be asked for a code from your authenticator app.</p> : null}
        <div className="flex flex-wrap gap-3">
          <SubmitButton busy={busy} busyLabel="Working…">{`Confirm: ${COMPLIANCE_ACTION_LABELS[action]}`}</SubmitButton>
          <Button variant="outline" onClick={onClose}>Cancel</Button>
        </div>
      </form>
    </Dialog>
  );
}

export function ComplianceCaseView({ complianceCase, meId, api = verificationApi }: { complianceCase: ComplianceCase; meId: string; api?: VerificationApi }) {
  const router = useRouter();
  const [action, setAction] = useState<ComplianceAction | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const { formError: noteFeedbackFormError, fieldErrors: noteFeedbackFieldErrors, errorRef: noteFeedbackErrorRef, formRef: noteFeedbackFormRef, clear: noteFeedbackClear, fail: noteFeedbackFail, failWith: noteFeedbackFailWith } = useVerificationFeedback();
  const noticeRef = useRef<HTMLDivElement>(null);
  const c = complianceCase;
  const assignedToMe = c.assigned_to?.id === meId;
  const resolutionPending = c.resolution?.resolution_status === "PROPOSED";
  const actions = c.allowed_actions ?? deriveComplianceActions({ status: c.status, assignedToMe, resolutionPending, proposedByMe: c.resolution?.proposed_by?.id === meId });
  const status = complianceStatusLabel(c.status);

  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);

  async function addNote(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const text = String(new FormData(form).get("body") ?? "").trim();
    if (text.length < 3) return noteFeedbackFail("Please correct the errors below.", { body: "Write a note." });
    noteFeedbackClear();
    setBusy(true);
    try {
      await api.complianceAction(c.id, "notes", { body: text });
      form.reset();
      setNotice("Note added.");
      router.refresh();
    } catch (error) {
      noteFeedbackFailWith(error, "The note could not be saved.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-6">
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      <Card as="section" aria-labelledby="cc-summary">
        <h2 id="cc-summary" className="font-display text-xl font-semibold text-ink-900">Case</h2>
        <div className="mt-4">
          <DefinitionList
            items={[
              { term: "Status", value: <Badge tone={status.tone}>{status.label}</Badge> },
              { term: "Type", value: humanise(c.case_type) },
              { term: "Severity", value: humanise(c.severity) },
              { term: "Subject", value: c.subject ? c.subject.display_name ?? `${humanise(c.subject.type)} ${c.subject.id}` : "—" },
              { term: "Assigned to", value: c.assigned_to ? `${c.assigned_to.display_name}${assignedToMe ? " (you)" : ""}` : "Unassigned" },
              { term: "Opened", value: formatDateTime(c.opened_at) },
            ]}
          />
        </div>
        {c.summary ? <p className="mt-4 whitespace-pre-line break-words text-ink-700">{c.summary}</p> : null}
        {c.resolution ? (
          <Alert tone="info" title={`Resolution ${c.resolution.resolution_status === "APPROVED" ? "approved" : "proposed"}: ${humanise(c.resolution.decision)}`} className="mt-4">
            Proposed by {c.resolution.proposed_by?.display_name ?? "—"}
            {c.resolution.approved_by ? `; approved by ${c.resolution.approved_by.display_name}` : "; waiting for approval by a different officer"}.
          </Alert>
        ) : null}
      </Card>

      <Card as="section" aria-labelledby="cc-actions">
        <h2 id="cc-actions" className="font-display text-xl font-semibold text-ink-900">Actions</h2>
        {actions.length === 0 ? (
          <p className="mt-2 text-ink-700">No actions are available to you right now.</p>
        ) : (
          <div className="mt-4 flex flex-wrap gap-3">
            {actions.map((a) => (
              <Button key={a} variant={a === "assign" || a === "start" ? "secondary" : "outline"} onClick={() => { setNotice(null); setAction(a); }}>
                {COMPLIANCE_ACTION_LABELS[a]}
              </Button>
            ))}
          </div>
        )}
      </Card>

      {c.links.length > 0 ? (
        <Card as="section" aria-labelledby="cc-links">
          <h2 id="cc-links" className="font-display text-xl font-semibold text-ink-900">Linked records</h2>
          <ul className="mt-2 list-disc space-y-1 pl-6 text-ink-700">
            {c.links.map((l) => (
              <li key={`${l.type}-${l.id}`}>
                {humanise(l.type)}: {l.label ?? l.id}
              </li>
            ))}
          </ul>
        </Card>
      ) : null}

      <Card as="section" aria-labelledby="cc-notes">
        <h2 id="cc-notes" className="font-display text-xl font-semibold text-ink-900">Notes (compliance only)</h2>
        {c.notes.length === 0 ? (
          <p className="mt-2 text-ink-700">No notes yet.</p>
        ) : (
          <ul className="mt-3 divide-y divide-line">
            {c.notes.map((n) => (
              <li key={n.id} className="py-2">
                <p className="text-sm text-ink-600">{n.author?.display_name ?? "—"} · {formatDateTime(n.created_at)}</p>
                <p className="mt-1 whitespace-pre-line break-words text-ink-900">{n.body}</p>
              </li>
            ))}
          </ul>
        )}
        <form ref={noteFeedbackFormRef} noValidate onSubmit={addNote} className="mt-4 space-y-4">
          <FormError ref={noteFeedbackErrorRef} message={noteFeedbackFormError} />
          <TextAreaField label="Add a note" name="body" error={noteFeedbackFieldErrors.body} maxLength={4000} />
          <SubmitButton busy={busy} busyLabel="Saving…">Add note</SubmitButton>
        </form>
      </Card>

      <Card as="section" aria-labelledby="cc-history">
        <h2 id="cc-history" className="font-display text-xl font-semibold text-ink-900">History</h2>
        {c.events.length === 0 ? (
          <p className="mt-2 text-ink-700">No events yet.</p>
        ) : (
          <ol className="mt-3 divide-y divide-line">
            {c.events.map((e) => (
              <li key={e.id} className="py-2 text-ink-700">
                <p className="flex flex-wrap justify-between gap-2">
                  <span className="font-semibold text-ink-900">{humanise(e.action)}</span>
                  <span className="text-sm text-ink-600">{formatDateTime(e.occurred_at)}</span>
                </p>
                <p className="text-sm">{e.actor?.display_name ?? "System"}{e.reason_code ? ` · ${humanise(e.reason_code)}` : ""}</p>
              </li>
            ))}
          </ol>
        )}
      </Card>

      {action ? (
        <ActionDialog
          action={action}
          caseId={c.id}
          api={api}
          onClose={() => setAction(null)}
          onDone={() => {
            const done = action;
            setAction(null);
            setNotice(`${COMPLIANCE_ACTION_LABELS[done]}: done.`);
            router.refresh();
          }}
        />
      ) : null}
    </div>
  );
}
