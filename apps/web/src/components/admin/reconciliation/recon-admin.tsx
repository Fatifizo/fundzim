"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent, type RefObject } from "react";

import { OpsAction } from "@/components/admin/operations/ops-action";
import { StepUpCancelledError, useStepUp } from "@/components/auth/step-up";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SelectField, TextAreaField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api/errors";
import { describeOpsError } from "@/lib/operations/errors";
import { EVIDENCE_REF, RESOLUTION_TYPES, SOURCES, type ExceptionItem, type Resolution, type RuleSet } from "@/lib/reconciliation/admin";
import { exportHref, reconciliationApi, type ProposalBody, type ReconciliationApi } from "@/lib/reconciliation/api";
import { checkStatement, STATEMENT_ACCEPT, STATEMENT_PROBLEMS, uploadStatement } from "@/lib/reconciliation/upload";

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const len = (s: string) => [...s].length;
const NOTE = { kind: "textarea", name: "note", label: "Note (staff only)", min: 5, max: 2000 } as const;
const CURRENCIES = [
  { value: "USD", label: "USD" },
  { value: "ZWG", label: "ZWG" },
];

/** Shared form state: busy, error with focus, notice with focus. */
function useFormState() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [notice, setNotice] = useState<string | null>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  const errorRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);
  const fail = (err: unknown, fallback: string) => {
    if (err instanceof StepUpCancelledError) return;
    const d = describeOpsError(err, fallback);
    if (d.field) setFieldErrors({ [d.field]: d.message });
    setError(d.message);
    requestAnimationFrame(() => errorRef.current?.focus());
  };
  const invalid = (errs: Record<string, string>) => {
    setFieldErrors(errs);
    if (Object.keys(errs).length === 0) return false;
    setError("Please correct the errors below.");
    requestAnimationFrame(() => errorRef.current?.focus());
    return true;
  };
  return { busy, setBusy, error, setError, fieldErrors, notice, setNotice, noticeRef, errorRef, fail, invalid };
}

function Messages({ notice, error, noticeRef, errorRef }: { notice: string | null; error: string | null; noticeRef: RefObject<HTMLDivElement | null>; errorRef: RefObject<HTMLDivElement | null> }) {
  return (
    <>
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      <FormError ref={errorRef} message={error} />
    </>
  );
}

/**
 * Import a TEST statement file (reconciliation.import + step-up). The file is hashed, deduplicated, scanned and parsed by
 * the API, all or nothing; the page lists its status. Never a real provider or bank statement in Stage 17.
 */
export function StatementUploadForm({ upload = uploadStatement }: { upload?: typeof uploadStatement }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const { busy, setBusy, error, setError, fieldErrors, notice, setNotice, noticeRef, errorRef, fail, invalid } = useFormState();
  const fileRef = useRef<HTMLInputElement>(null);
  const [importId, setImportId] = useState<string | null>(null);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const f = new FormData(form);
    const source = String(f.get("source") ?? "");
    const supersedes = String(f.get("supersedes_import_id") ?? "").trim();
    const file = fileRef.current?.files?.[0];
    const errs: Record<string, string> = {};
    if (!SOURCES.some((x) => x.code === source)) errs.source = "Choose the statement's source.";
    if (!file) errs.file = "Choose a .csv or .json statement file.";
    else {
      const p = checkStatement(file);
      if (p) errs.file = STATEMENT_PROBLEMS[p];
    }
    if (supersedes && !UUID.test(supersedes)) errs.supersedes_import_id = "Paste the full import id, or leave it empty.";
    setNotice(null);
    if (invalid(errs) || !file) return;
    setBusy(true);
    setError(null);
    try {
      const res = await withStepUp(() => upload({ source, file, supersedesImportId: supersedes || undefined }));
      form.reset();
      setImportId(res.id ?? null);
      setNotice("Statement received. It is scanned and parsed in the background.");
      router.refresh();
    } catch (err) {
      fail(err, "The statement could not be imported.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form noValidate onSubmit={submit} className="space-y-4">
      <Messages notice={notice} error={error} noticeRef={noticeRef} errorRef={errorRef} />
      {notice && importId ? (
        <p>
          <Link href={`/admin/reconciliation/batches/${importId}`} className="font-semibold text-brand-700 underline">
            View the import
          </Link>
        </p>
      ) : null}
      <SelectField label="Source" name="source" placeholderOption="Choose…" options={SOURCES.map((x) => ({ value: x.code, label: x.label }))} error={fieldErrors.source} />
      <div className="space-y-1">
        <label htmlFor="statement-file" className="block font-semibold text-ink-900">
          Statement file (.csv or .json, canonical format, at most 10 MB)
        </label>
        <input
          ref={fileRef}
          id="statement-file"
          name="file"
          type="file"
          accept={STATEMENT_ACCEPT}
          aria-invalid={fieldErrors.file ? true : undefined}
          aria-describedby={fieldErrors.file ? "statement-file-error" : undefined}
          className="block w-full text-ink-900"
        />
        {fieldErrors.file ? (
          <p id="statement-file-error" className="text-sm font-semibold text-danger-700">
            <span aria-hidden="true">Error: </span>
            {fieldErrors.file}
          </p>
        ) : null}
      </div>
      <TextField label="Supersedes import (optional)" name="supersedes_import_id" autoComplete="off" spellCheck={false}
        hint="Only for a corrected re-issue of a statement already parsed from the same source." error={fieldErrors.supersedes_import_id} />
      <SubmitButton busy={busy} busyLabel="Uploading…">
        Import statement
      </SubmitButton>
    </form>
  );
}

/** Build a TEST statement from the sandbox simulator's own records for a recent period (reconciliation.import + step-up). */
export function SandboxStatementForm({ api = reconciliationApi }: { api?: ReconciliationApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const { busy, setBusy, error, setError, fieldErrors, notice, setNotice, noticeRef, errorRef, fail, invalid } = useFormState();

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const f = new FormData(event.currentTarget);
    const source = String(f.get("source") ?? "");
    const currency = String(f.get("currency") ?? "");
    const hoursRaw = String(f.get("hours") ?? "").trim();
    const hours = /^\d{1,4}$/.test(hoursRaw) ? Number.parseInt(hoursRaw, 10) : NaN;
    const errs: Record<string, string> = {};
    if (!source.startsWith("sandbox_")) errs.source = "Choose a sandbox source.";
    if (!(hours >= 1 && hours <= 2232)) errs.hours = "Enter 1 to 2,232 hours (93 days).";
    setNotice(null);
    if (invalid(errs)) return;
    setBusy(true);
    setError(null);
    const end = new Date();
    const start = new Date(end.getTime() - hours * 3600 * 1000);
    const key = crypto.randomUUID();
    try {
      await withStepUp(() => api.sandboxStatement({ source, currency, period_start: start.toISOString(), period_end: end.toISOString() }, key));
      setNotice("TEST statement generated from the simulator. It is scanned and parsed in the background.");
      router.refresh();
    } catch (err) {
      fail(err, "The statement could not be generated.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form noValidate onSubmit={submit} className="space-y-4">
      <Messages notice={notice} error={error} noticeRef={noticeRef} errorRef={errorRef} />
      <SelectField label="Sandbox source" name="source" placeholderOption="Choose…"
        options={SOURCES.filter((x) => x.code.startsWith("sandbox_")).map((x) => ({ value: x.code, label: x.label }))} error={fieldErrors.source} />
      <div className="grid gap-4 sm:grid-cols-2">
        <SelectField label="Currency" name="currency" defaultValue="USD" options={CURRENCIES} />
        <TextField label="Covering the last (hours)" name="hours" inputMode="numeric" defaultValue="24" error={fieldErrors.hours} />
      </div>
      <SubmitButton busy={busy} busyLabel="Working…">
        Generate TEST statement
      </SubmitButton>
    </form>
  );
}

/** Queue an on-demand matching run (reconciliation.run). Reason required; the worker executes it. */
export function RunRequestForm({ api = reconciliationApi }: { api?: ReconciliationApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const { busy, setBusy, error, setError, fieldErrors, notice, setNotice, noticeRef, errorRef, fail, invalid } = useFormState();

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const f = new FormData(form);
    const source = String(f.get("source") ?? "");
    const currency = String(f.get("currency") ?? "");
    const reason = String(f.get("reason") ?? "").trim();
    const errs: Record<string, string> = {};
    if (!SOURCES.some((x) => x.code === source)) errs.source = "Choose a source.";
    if (len(reason) < 5 || len(reason) > 500) errs.reason = "Write 5 to 500 characters.";
    setNotice(null);
    if (invalid(errs)) return;
    setBusy(true);
    setError(null);
    try {
      await withStepUp(() => api.requestRun({ source, currency, reason }, crypto.randomUUID()));
      form.reset();
      setNotice("Run queued. Results appear here when it finishes.");
      router.refresh();
    } catch (err) {
      fail(err, "The run could not be queued.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form noValidate onSubmit={submit} className="space-y-4">
      <Messages notice={notice} error={error} noticeRef={noticeRef} errorRef={errorRef} />
      <div className="grid gap-4 sm:grid-cols-2">
        <SelectField label="Source" name="source" placeholderOption="Choose…" options={SOURCES.map((x) => ({ value: x.code, label: x.label }))} error={fieldErrors.source} />
        <SelectField label="Currency" name="currency" defaultValue="USD" options={CURRENCIES} />
      </div>
      <TextAreaField label="Reason (staff only)" name="reason" required maxLength={500} error={fieldErrors.reason} />
      <SubmitButton busy={busy} busyLabel="Working…">
        Queue run
      </SubmitButton>
    </form>
  );
}

/** Replay a finished run's scope (a new run; never duplicates a match or a case, never posts). */
export function ReplayAction({ runId, api = reconciliationApi }: { runId: string; api?: ReconciliationApi }) {
  return (
    <OpsAction button="Replay" title="Replay this run?" description="A new run decides the same source, currency and window again with the active rule set. Matches and exceptions are never duplicated."
      confirm="Confirm: Replay" notice="Replay queued." fields={[{ kind: "textarea", name: "reason", label: "Reason (staff only)", min: 5, max: 500 }]}
      run={(b) => api.requestRun({ replay_of_run_id: runId, reason: String(b.reason) }, crypto.randomUUID())} />
  );
}

/** Lifecycle actions on an exception; the API refuses anything the caller may not do (permission, state, version). */
export function ExceptionActions({ exc, api = reconciliationApi }: { exc: ExceptionItem; api?: ReconciliationApi }) {
  const st = exc.status;
  const v = { version: exc.version };
  return (
    <div className="flex flex-wrap gap-2">
      <OpsAction button={exc.assignedTo ? "Reassign" : "Assign"} variant="primary" title="Assign an investigator" description="The person needs the permission to investigate reconciliation exceptions. A due date is optional; no service level is configured."
        confirm="Confirm: Assign" notice="Assigned." body={v} available={["OPEN", "REOPENED", "ASSIGNED", "INVESTIGATING", "AWAITING_EVIDENCE"].includes(st)}
        fields={[{ kind: "text", name: "assignee_id", label: "Staff member id", uuid: true }, { kind: "text", name: "due_date", label: "Due date (YYYY-MM-DD, optional)", optional: true }]}
        run={(b) => {
          const due = String(b.due_date ?? "");
          const body: Record<string, unknown> = { version: exc.version, assignee_id: b.assignee_id };
          if (/^\d{4}-\d{2}-\d{2}$/.test(due)) body.due_at = new Date(`${due}T17:00:00+02:00`).toISOString();
          return api.act(exc.id, "assign", body);
        }} />
      <OpsAction button="Start investigation" title="Start the investigation?" description="Only the assigned investigator can start it." confirm="Confirm: Start"
        notice="Investigation started." body={v} fields={[NOTE]} available={st === "ASSIGNED"} run={(b) => api.act(exc.id, "start", b)} />
      <OpsAction button="Wait for evidence" title="Wait for evidence?" description="Record what you asked for and from whom (no personal data)." confirm="Confirm: Waiting"
        notice="Marked as waiting for evidence." body={v} fields={[NOTE]} available={st === "INVESTIGATING"} run={(b) => api.act(exc.id, "await-evidence", b)} />
      <OpsAction button="Resume" title="Resume the investigation?" description="The evidence arrived or the wait ended." confirm="Confirm: Resume" notice="Investigation resumed."
        body={v} fields={[NOTE]} available={st === "AWAITING_EVIDENCE"} run={(b) => api.act(exc.id, "resume", b)} />
      <OpsAction button="Add note or evidence reference" title="Add to the history" description="Evidence references are ids such as import:<id> or document:<id>, never files or personal data."
        confirm="Confirm: Add" notice="Added to the history." fields={[NOTE, { kind: "text", name: "evidence_ref", label: "Evidence reference (optional)", optional: true }]}
        available={st !== "CLOSED"} run={(b) => api.act(exc.id, "comment", b)} />
      <OpsAction button="Close" title="Close this resolved exception?" description="Closing records that the resolution was verified." confirm="Confirm: Close" notice="Closed."
        body={v} fields={[NOTE]} available={st === "RESOLVED"} run={(b) => api.act(exc.id, "close", b)} />
      <OpsAction button="Reopen" title="Reopen this exception?" description="For late or contradicting evidence. It returns to the queue for assignment." confirm="Confirm: Reopen"
        notice="Reopened." body={v} fields={[NOTE]} available={st === "RESOLVED"} run={(b) => api.act(exc.id, "reopen", b)} />
    </div>
  );
}

/** Propose a resolution (maker; reconciliation.investigate). A second person approves it; nothing changes before. */
export function ProposalForm({ exc, api = reconciliationApi }: { exc: ExceptionItem; api?: ReconciliationApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const { busy, setBusy, error, setError, fieldErrors, notice, setNotice, noticeRef, errorRef, fail, invalid } = useFormState();
  const [type, setType] = useState("");
  const help = RESOLUTION_TYPES.find((x) => x.type === type)?.help;

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const f = new FormData(event.currentTarget);
    const justification = String(f.get("justification") ?? "").trim();
    const refs = String(f.get("evidence_refs") ?? "").split(/[\s,]+/).map((x) => x.trim()).filter(Boolean);
    const errs: Record<string, string> = {};
    if (!RESOLUTION_TYPES.some((x) => x.type === type)) errs.resolution_type = "Choose a resolution.";
    if (len(justification) < 20 || len(justification) > 2000) errs.justification = "Write 20 to 2,000 characters.";
    if (refs.length < 1 || refs.length > 10 || refs.some((r) => !EVIDENCE_REF.test(r))) errs.evidence_refs = "Give 1 to 10 references such as import:<id> or item:<id>.";
    const body: ProposalBody = { resolution_type: type, justification, evidence_refs: refs, version: exc.version };
    if (type === "MANUAL_MATCH") {
      body.subject_type = String(f.get("subject_type") ?? "");
      body.subject_id = String(f.get("subject_id") ?? "").trim();
      if (!["PAYMENT", "PAYOUT", "FEE_REMITTANCE_LINE"].includes(body.subject_type)) errs.subject_type = "Choose what the line is.";
      if (!UUID.test(body.subject_id)) errs.subject_id = "Paste the full record id.";
    }
    if (type === "ESCALATED_TO_CASE") {
      body.case_ref = String(f.get("case_ref") ?? "").trim();
      if (!body.case_ref || len(body.case_ref) > 100) errs.case_ref = "Give the case reference.";
    }
    if (type === "LEDGER_ADJUSTMENT") {
      body.posting_rule = String(f.get("posting_rule") ?? "");
      const lines = String(f.get("lines") ?? "").split("\n").map((l) => l.trim()).filter(Boolean).map((l) => l.split(",").map((x) => x.trim()));
      body.lines = lines.map(([account_code = "", direction = "", amount_minor = ""]) => ({ account_code, direction: direction.toUpperCase(), amount_minor }));
      if (!["SETTLEMENT_DISCREPANCY_RESOLVED", "ADJUSTMENT", "WRITE_OFF"].includes(body.posting_rule)) errs.posting_rule = "Choose a rule.";
      if (body.lines.length < 2 || body.lines.length > 20 || body.lines.some((l) => !/^(DEBIT|CREDIT)$/.test(l.direction) || !/^[1-9]\d{0,17}$/.test(l.amount_minor) || !l.account_code))
        errs.lines = "One line per entry: account code, DEBIT or CREDIT, amount in minor units (at least two lines).";
    }
    setNotice(null);
    if (invalid(errs)) return;
    setBusy(true);
    setError(null);
    try {
      await withStepUp(() => api.propose(exc.id, body, crypto.randomUUID()));
      setNotice("Resolution proposed. A second team member must approve it within 24 hours.");
      router.refresh();
    } catch (err) {
      fail(err, "The resolution could not be proposed.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form noValidate onSubmit={submit} className="space-y-4">
      <Messages notice={notice} error={error} noticeRef={noticeRef} errorRef={errorRef} />
      <SelectField label="Resolution" name="resolution_type" placeholderOption="Choose…" value={type} onChange={(e) => setType(e.target.value)}
        options={RESOLUTION_TYPES.map((x) => ({ value: x.type, label: x.label }))} hint={help} error={fieldErrors.resolution_type} />
      {type === "MANUAL_MATCH" ? (
        <div className="grid gap-4 sm:grid-cols-2">
          <SelectField label="The line is" name="subject_type" placeholderOption="Choose…" error={fieldErrors.subject_type}
            options={[{ value: "PAYMENT", label: "A payment" }, { value: "PAYOUT", label: "A payout" }, { value: "FEE_REMITTANCE_LINE", label: "A fee remittance line" }]} />
          <TextField label="Record id" name="subject_id" autoComplete="off" spellCheck={false} error={fieldErrors.subject_id} />
        </div>
      ) : null}
      {type === "ESCALATED_TO_CASE" ? <TextField label="Case reference" name="case_ref" autoComplete="off" error={fieldErrors.case_ref} /> : null}
      {type === "LEDGER_ADJUSTMENT" ? (
        <>
          <SelectField label="Ledger rule" name="posting_rule" placeholderOption="Choose…" error={fieldErrors.posting_rule}
            options={["SETTLEMENT_DISCREPANCY_RESOLVED", "ADJUSTMENT", "WRITE_OFF"].map((r) => ({ value: r, label: r }))} />
          <TextAreaField label="Journal lines" name="lines" required maxLength={4000} error={fieldErrors.lines}
            hint="One per line: account code, DEBIT or CREDIT, amount in minor units — e.g. suspense:settlement_discrepancy:<provider id>, DEBIT, 35. Debits must equal credits in one currency; the ledger checks." />
        </>
      ) : null}
      <TextAreaField label="Justification (staff only, 20–2,000 characters)" name="justification" required maxLength={2000} error={fieldErrors.justification} />
      <TextField label="Evidence references" name="evidence_refs" autoComplete="off" spellCheck={false} defaultValue={exc.itemId ? `item:${exc.itemId}` : ""}
        hint="Separated by spaces or commas: import:<id>, item:<id>, batch:<id>, evidence:<id>, document:<id>." error={fieldErrors.evidence_refs} />
      <SubmitButton busy={busy} busyLabel="Working…">
        Propose resolution
      </SubmitButton>
    </form>
  );
}

/** Checker decisions on a resolution. The approval binds the proposal hash shown on the page. */
export function ResolutionActions({ res, api = reconciliationApi }: { res: Resolution; api?: ReconciliationApi }) {
  const pending = res.status === "PENDING_REVIEW";
  return (
    <div className="flex flex-wrap gap-2">
      <OpsAction button="Approve and apply" variant="primary" title="Approve this resolution?" description="It is applied at once by the module that owns the record (or by the ledger's own approval). You cannot approve your own proposal."
        confirm="Confirm: Approve" notice="Resolution approved and applied." fields={[NOTE]} body={{ proposal_sha256: res.sha256 }} available={pending}
        run={(b) => api.decide(res.id, "approve", { note: String(b.note), proposal_sha256: res.sha256 })} />
      <OpsAction button="Reject" title="Reject this resolution?" description="The exception returns to investigation." confirm="Confirm: Reject" notice="Resolution rejected."
        fields={[NOTE]} available={pending} run={(b) => api.decide(res.id, "reject", { note: String(b.note) })} />
      <OpsAction button="Cancel proposal" title="Cancel your proposal?" description="Only the proposer can cancel it." confirm="Confirm: Cancel proposal" notice="Proposal cancelled."
        fields={[NOTE]} available={pending} run={(b) => api.decide(res.id, "cancel", { note: String(b.note) })} />
    </div>
  );
}

const RULE_FIELDS = [
  { key: "date_window_days", label: "Date window (days)", min: 0, max: 14, hint: "How far a line's booking may be from the record's time for a one-reference match." },
  { key: "settlement_horizon_days", label: "Settlement horizon (days)", min: 1, max: 60, hint: "After this, a record inside a statement's period must appear on it." },
  { key: "stale_after_hours", label: "Stale after (hours)", min: 1, max: 2160, hint: "A used source without a new statement for this long opens STALE_RECONCILIATION." },
  { key: "escalate_after_hours", label: "Escalate after (hours, optional)", min: 1, max: 2160, hint: "Leave empty for no automatic escalation." },
] as const;

/** Propose a matching rule set (maker). The values are engineering choices for TEST statements, not confirmed schedules. */
export function RuleSetForm({ current, api = reconciliationApi }: { current: RuleSet | null; api?: ReconciliationApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const { busy, setBusy, error, setError, fieldErrors, notice, setNotice, noticeRef, errorRef, fail, invalid } = useFormState();

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const f = new FormData(event.currentTarget);
    const errs: Record<string, string> = {};
    const config: Record<string, number> = {};
    for (const r of RULE_FIELDS) {
      const raw = String(f.get(r.key) ?? "").trim();
      if (raw === "" && r.key === "escalate_after_hours") continue;
      const n = /^\d{1,4}$/.test(raw) ? Number.parseInt(raw, 10) : NaN;
      if (!(n >= r.min && n <= r.max)) errs[r.key] = `Enter a whole number from ${r.min} to ${r.max}.`;
      config[r.key] = n;
    }
    const reason = String(f.get("change_reason") ?? "").trim();
    if (len(reason) < 10 || len(reason) > 1000) errs.change_reason = "Write 10 to 1,000 characters.";
    setNotice(null);
    if (invalid(errs)) return;
    setBusy(true);
    setError(null);
    try {
      await withStepUp(() => api.proposeRules({ config, change_reason: reason }, crypto.randomUUID()));
      setNotice("Rule set proposed. A second team member must approve it before matching uses it.");
      router.refresh();
    } catch (err) {
      fail(err, "The rule set could not be proposed.");
    } finally {
      setBusy(false);
    }
  }

  const value = (k: string) => {
    if (!current) return "";
    const m: Record<string, number | null> = { date_window_days: current.dateWindowDays, settlement_horizon_days: current.settlementHorizonDays,
      stale_after_hours: current.staleAfterHours, escalate_after_hours: current.escalateAfterHours };
    return m[k] == null ? "" : String(m[k]);
  };
  return (
    <form noValidate onSubmit={submit} className="space-y-4">
      <Messages notice={notice} error={error} noticeRef={noticeRef} errorRef={errorRef} />
      <div className="grid gap-4 sm:grid-cols-2">
        {RULE_FIELDS.map((r) => (
          <TextField key={r.key} label={r.label} name={r.key} inputMode="numeric" defaultValue={value(r.key)} hint={r.hint} error={fieldErrors[r.key]} />
        ))}
      </div>
      <TextAreaField label="Why (staff only)" name="change_reason" required maxLength={1000} error={fieldErrors.change_reason} />
      <SubmitButton busy={busy} busyLabel="Working…">
        Propose rule set
      </SubmitButton>
    </form>
  );
}

export function RuleSetActions({ rs, api = reconciliationApi }: { rs: RuleSet; api?: ReconciliationApi }) {
  const pending = rs.status === "PENDING_APPROVAL";
  return (
    <div className="flex flex-wrap gap-2">
      <OpsAction button="Approve" variant="primary" title="Approve and activate this rule set?" description="It replaces the active set for new runs; earlier decisions keep their version. You cannot approve your own proposal."
        confirm="Confirm: Approve" notice="Rule set approved and active." fields={[NOTE]} available={pending}
        run={(b) => api.decideRules(rs.id, "approve", { note: String(b.note), config_sha256: rs.sha256 })} />
      <OpsAction button="Reject" title="Reject this rule set?" description="The active set stays." confirm="Confirm: Reject" notice="Rule set rejected." fields={[NOTE]}
        available={pending} run={(b) => api.decideRules(rs.id, "reject", { note: String(b.note) })} />
      <OpsAction button="Withdraw" title="Withdraw your proposal?" description="Only the proposer can withdraw it." confirm="Confirm: Withdraw" notice="Proposal withdrawn."
        fields={[NOTE]} available={pending} run={(b) => api.decideRules(rs.id, "withdraw", { note: String(b.note) })} />
      <OpsAction button="Retire" title="Retire the active rule set?" description="Matching stops (fails closed) until another rule set is approved." confirm="Confirm: Retire"
        notice="Rule set retired." fields={[NOTE]} available={rs.status === "ACTIVE"} run={(b) => api.decideRules(rs.id, "retire", { note: String(b.note) })} />
    </div>
  );
}

/** Download a report as CSV (reconciliation.report.export + step-up; audited by the API). */
export function ExportButton({ kind, label = "Export CSV" }: { kind: string; label?: string }) {
  const withStepUp = useStepUp();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function run() {
    setBusy(true);
    setError(null);
    try {
      const blob = await withStepUp(async () => {
        const res = await fetch(exportHref(kind), { credentials: "same-origin", headers: { Accept: "text/csv" } });
        if (!res.ok) {
          let code = "UNKNOWN";
          try {
            const body = (await res.json()) as { error?: { code?: string } };
            code = body.error?.code ?? code;
          } catch {
            // non-JSON error body
          }
          throw new ApiError({ code, message: "", status: res.status, requestId: res.headers.get("X-Request-ID") ?? "", retryable: false });
        }
        return res.blob();
      });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `fundzim-reconciliation-${kind.replace(/_/g, "-")}-TEST.csv`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) setError(describeOpsError(err, "The export failed.").message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="space-y-2">
      <Button variant="outline" onClick={run} disabled={busy}>
        {busy ? "Exporting…" : label}
      </Button>
      {error ? (
        <p role="alert" className="text-sm font-semibold text-danger-700">
          {error}
        </p>
      ) : null}
    </div>
  );
}
