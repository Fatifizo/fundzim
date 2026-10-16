"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent, type ReactNode, type Ref } from "react";

import { StepUpCancelledError, useStepUp } from "@/components/auth/step-up";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SelectField, TextAreaField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { humanise } from "@/lib/payments/labels";
import { downloadAuditExport, opsApi, type OpsApi } from "@/lib/operations/api";
import { describeOpsError } from "@/lib/operations/errors";
import { ESCALATION_TARGETS, SEVERITIES, SLA_METRICS, SUPPORT_CATEGORIES, SUPPORT_PRIORITIES, SUPPORT_REFERENCE_TYPES } from "@/lib/operations/types";

const opts = (values: readonly string[]) => values.map((v) => ({ value: v, label: humanise(v) }));
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const len = (s: string) => [...s].length;
const val = (f: FormData, k: string) => String(f.get(k) ?? "").trim();

/** Shared submit plumbing: one Idempotency-Key per distinct body, step-up retry, focus on the notice or the error. */
function useOpsSubmit() {
  const router = useRouter();
  const withStepUp = useStepUp();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const errorRef = useRef<HTMLDivElement>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  const keyRef = useRef<{ body: string; key: string } | null>(null);
  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);
  const fail = (message: string, errs: Record<string, string> = {}) => {
    setFieldErrors(errs);
    setError(message);
    requestAnimationFrame(() => errorRef.current?.focus());
  };
  async function run<T>(body: unknown, call: (key: string) => Promise<T>, done: (r: T) => string | void, refresh = true) {
    setFieldErrors({});
    setError(null);
    setNotice(null);
    const text = JSON.stringify(body);
    if (!keyRef.current || keyRef.current.body !== text) keyRef.current = { body: text, key: crypto.randomUUID() };
    setBusy(true);
    try {
      const r = await withStepUp(() => call(keyRef.current!.key));
      keyRef.current = null;
      const msg = done(r);
      if (msg) setNotice(msg);
      if (refresh) router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) {
        const d = describeOpsError(err);
        fail(d.message, d.field ? { [d.field]: d.message } : {});
      }
    } finally {
      setBusy(false);
    }
  }
  return { busy, error, notice, fieldErrors, errorRef, noticeRef, fail, run, setFieldErrors };
}

interface ShellProps {
  onSubmit: (e: FormEvent<HTMLFormElement>) => void;
  children: ReactNode;
  submit: string;
  label: string;
  busy: boolean;
  error: string | null;
  notice: string | null;
  errorRef: Ref<HTMLDivElement>;
  noticeRef: Ref<HTMLDivElement>;
}

function Shell({ onSubmit, children, submit, label, busy, error, notice, errorRef, noticeRef }: ShellProps) {
  return (
    <form noValidate onSubmit={onSubmit} aria-label={label} className="space-y-5 rounded-card border border-line bg-surface p-4">
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      <FormError ref={errorRef} message={error} />
      {children}
      <SubmitButton busy={busy}>{submit}</SubmitButton>
    </form>
  );
}

/** Opens a support case (ops.support.work). No KYC data, bank details or document numbers belong in a case. */
export function OpenSupportCaseForm({ api = opsApi, onOpened }: { api?: OpsApi; onOpened?: (id: string) => void }) {
  const { busy, error, notice, fieldErrors, errorRef, noticeRef, fail, run } = useOpsSubmit();
  const router = useRouter();
  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const f = new FormData(e.currentTarget);
    const body: Record<string, string> = {};
    for (const k of ["subject", "category", "priority", "requester_user_id", "related_campaign_id", "related_reference_type", "related_reference_id", "note"]) {
      const v = val(f, k);
      if (v) body[k] = v;
    }
    const errs: Record<string, string> = {};
    if (!body.subject || len(body.subject) < 3 || len(body.subject) > 200) errs.subject = "Write 3 to 200 characters.";
    if (!body.category) errs.category = "Choose a category.";
    for (const k of ["requester_user_id", "related_campaign_id", "related_reference_id"]) if (body[k] && !UUID.test(body[k])) errs[k] = "Paste a full record id.";
    if (!!body.related_reference_type !== !!body.related_reference_id) errs.related_reference_id = "Give both the reference type and its id, or neither.";
    if (Object.keys(errs).length) return fail("Check the highlighted fields.", errs);
    void run(body, (key) => api.openSupportCase(body, key), (r) => {
      const id = (r as { id?: string })?.id;
      if (id) {
        if (onOpened) onOpened(id);
        else router.push(`/admin/support/${encodeURIComponent(id)}`);
      }
      return "Support case opened.";
    });
  }
  const fe = fieldErrors;
  return (
    <Shell busy={busy} error={error} notice={notice} errorRef={errorRef} noticeRef={noticeRef} onSubmit={submit} submit="Open support case" label="Open a support case">
      <TextField label="Subject" name="subject" maxLength={200} error={fe.subject} hint="A short description. No identity numbers, bank details or passwords." />
      <div className="grid gap-4 sm:grid-cols-2">
        <SelectField label="Category" name="category" placeholderOption="Choose…" options={opts(SUPPORT_CATEGORIES)} error={fe.category} />
        <SelectField label="Priority" name="priority" defaultValue="P3" options={opts(SUPPORT_PRIORITIES)} />
      </div>
      <TextField label="Requester account id (optional)" name="requester_user_id" autoComplete="off" error={fe.requester_user_id} />
      <TextField label="Related campaign id (optional)" name="related_campaign_id" autoComplete="off" error={fe.related_campaign_id} />
      <div className="grid gap-4 sm:grid-cols-2">
        <SelectField label="Related record type (optional)" name="related_reference_type" placeholderOption="None" options={opts(SUPPORT_REFERENCE_TYPES)} />
        <TextField label="Related record id (optional)" name="related_reference_id" autoComplete="off" error={fe.related_reference_id} />
      </div>
      <TextAreaField label="First note (optional, staff only)" name="note" maxLength={2000} />
    </Shell>
  );
}

/** Declares an incident (ops.incident.manage). From an alert, the alert key keeps one open incident per alert. */
export function DeclareIncidentForm({ api = opsApi, alertKey, defaultTitle, defaultModule }: { api?: OpsApi; alertKey?: string; defaultTitle?: string; defaultModule?: string }) {
  const { busy, error, notice, fieldErrors, errorRef, noticeRef, fail, run } = useOpsSubmit();
  const router = useRouter();
  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const f = new FormData(e.currentTarget);
    const body: Record<string, string> = { title: val(f, "title"), severity: val(f, "severity"), affected_module: val(f, "affected_module") };
    const note = val(f, "note");
    if (note) body.note = note;
    if (alertKey) body.alert_key = alertKey;
    const errs: Record<string, string> = {};
    if (len(body.title!) < 3 || len(body.title!) > 200) errs.title = "Write 3 to 200 characters.";
    if (!body.severity) errs.severity = "Choose a severity.";
    if (!/^[a-z][a-z_]{1,40}$/.test(body.affected_module!)) errs.affected_module = "Use the module name in lower case, for example payments or outbox.";
    if (Object.keys(errs).length) return fail("Check the highlighted fields.", errs);
    void run(body, (key) => api.declareIncident(body, key), (r) => {
      const id = (r as { id?: string })?.id;
      if (id) router.push(`/admin/operations/incidents/${encodeURIComponent(id)}`);
      return "Incident declared.";
    });
  }
  const fe = fieldErrors;
  return (
    <Shell busy={busy} error={error} notice={notice} errorRef={errorRef} noticeRef={noticeRef} onSubmit={submit} submit="Declare incident" label="Declare an incident">
      <TextField label="Title" name="title" defaultValue={defaultTitle} maxLength={200} error={fe.title} />
      <div className="grid gap-4 sm:grid-cols-2">
        <SelectField label="Severity" name="severity" placeholderOption="Choose…" options={opts(SEVERITIES)} error={fe.severity} />
        <TextField label="Affected module" name="affected_module" defaultValue={defaultModule} autoComplete="off" error={fe.affected_module} />
      </div>
      <TextAreaField label="First note (optional)" name="note" maxLength={2000} />
    </Shell>
  );
}

/** Proposes an internal service target (ops.sla.request). A different person (ops.sla.approve) must approve it. */
export function ProposeSLAForm({ api = opsApi }: { api?: OpsApi }) {
  const { busy, error, notice, fieldErrors, errorRef, noticeRef, fail, run } = useOpsSubmit();
  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const f = new FormData(e.currentTarget);
    const metric = val(f, "metric_key");
    const hours = val(f, "hours");
    const reason = val(f, "reason");
    const errs: Record<string, string> = {};
    if (!metric) errs.metric_key = "Choose a metric.";
    const n = /^\d{1,4}$/.test(hours) ? Number.parseInt(hours, 10) : NaN;
    if (!Number.isInteger(n) || n < 1 || n > 8760) errs.hours = "Give a whole number of hours from 1 to 8,760.";
    if (len(reason) < 5 || len(reason) > 2000) errs.reason = "Write 5 to 2,000 characters.";
    if (Object.keys(errs).length) return fail("Check the highlighted fields.", errs);
    const body = { metric_key: metric, target_seconds: n * 3600, reason };
    void run(body, (key) => api.proposeSLA(body, key), () => "Target proposed. It applies only after a different person approves it.");
  }
  const fe = fieldErrors;
  return (
    <Shell busy={busy} error={error} notice={notice} errorRef={errorRef} noticeRef={noticeRef} onSubmit={submit} submit="Propose target" label="Propose an internal service target">
      <SelectField label="Metric" name="metric_key" placeholderOption="Choose…" options={opts(SLA_METRICS)} error={fe.metric_key} />
      <TextField label="Target (hours)" name="hours" inputMode="numeric" autoComplete="off" error={fe.hours} hint="Internal operational target only — not a contractual or regulatory deadline." />
      <TextAreaField label="Reason" name="reason" required maxLength={2000} error={fe.reason} />
    </Shell>
  );
}

/** Starts an access review (access.review.run). The reviewer must be someone else; nobody certifies their own access. */
export function StartAccessReviewForm({ api = opsApi, roles }: { api?: OpsApi; roles: readonly string[] }) {
  const { busy, error, notice, fieldErrors, errorRef, noticeRef, fail, run } = useOpsSubmit();
  const router = useRouter();
  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const f = new FormData(e.currentTarget);
    const body: Record<string, string> = { title: val(f, "title"), reviewer_id: val(f, "reviewer_id") };
    const scope = val(f, "scope_role");
    if (scope) body.scope_role = scope;
    const errs: Record<string, string> = {};
    if (len(body.title!) < 3 || len(body.title!) > 200) errs.title = "Write 3 to 200 characters.";
    if (!UUID.test(body.reviewer_id!)) errs.reviewer_id = "Paste the reviewer's staff id.";
    if (Object.keys(errs).length) return fail("Check the highlighted fields.", errs);
    void run(body, (key) => api.startAccessReview(body, key), (r) => {
      const id = (r as { id?: string })?.id;
      if (id) router.push(`/admin/staff/access-reviews/${encodeURIComponent(id)}`);
      return "Access review started.";
    });
  }
  const fe = fieldErrors;
  return (
    <Shell busy={busy} error={error} notice={notice} errorRef={errorRef} noticeRef={noticeRef} onSubmit={submit} submit="Start access review" label="Start an access review">
      <TextField label="Title" name="title" maxLength={200} error={fe.title} />
      <SelectField label="Role in scope" name="scope_role" placeholderOption="Every role" options={opts(roles)} />
      <TextField label="Reviewer staff id" name="reviewer_id" autoComplete="off" error={fe.reviewer_id} hint="A staff member allowed to certify access. Not yourself." />
    </Shell>
  );
}

/** Controlled audit export (audit.export, step-up): at most 31 days and 5,000 records; the export itself is audited. */
export function AuditExportForm({ download = downloadAuditExport }: { download?: typeof downloadAuditExport }) {
  const { busy, error, notice, fieldErrors, errorRef, noticeRef, fail, run } = useOpsSubmit();
  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const f = new FormData(e.currentTarget);
    const from = val(f, "from");
    const to = val(f, "to");
    const errs: Record<string, string> = {};
    const fromD = new Date(`${from}T00:00:00Z`);
    const toD = new Date(`${to}T23:59:59Z`);
    if (!from || Number.isNaN(fromD.getTime())) errs.from = "Choose a start date.";
    if (!to || Number.isNaN(toD.getTime())) errs.to = "Choose an end date.";
    if (!errs.from && !errs.to && (toD <= fromD || toD.getTime() - fromD.getTime() > 31 * 86400 * 1000)) errs.to = "The range must be 1 to 31 days.";
    if (Object.keys(errs).length) return fail("Check the highlighted fields.", errs);
    const body: Record<string, string> = { format: val(f, "format") || "csv", from: fromD.toISOString(), to: toD.toISOString() };
    for (const k of ["action", "target_type", "correlation_id"]) {
      const v = val(f, k);
      if (v) body[k] = v;
    }
    void run(
      body,
      () => download(body),
      (r) => {
        const url = URL.createObjectURL(r.blob);
        const a = document.createElement("a");
        a.href = url;
        a.download = r.filename;
        a.click();
        URL.revokeObjectURL(url);
        return `Export downloaded (${r.filename}). The export is recorded in the security audit log.`;
      },
      false,
    );
  }
  const fe = fieldErrors;
  return (
    <Shell busy={busy} error={error} notice={notice} errorRef={errorRef} noticeRef={noticeRef} onSubmit={submit} submit="Export" label="Export audit records">
      <div className="grid gap-4 sm:grid-cols-3">
        <TextField label="From (date)" name="from" type="date" error={fe.from} />
        <TextField label="To (date)" name="to" type="date" error={fe.to} />
        <SelectField label="Format" name="format" options={[{ value: "csv", label: "CSV" }, { value: "json", label: "JSON" }]} />
      </div>
      <div className="grid gap-4 sm:grid-cols-3">
        <TextField label="Action prefix (optional)" name="action" autoComplete="off" />
        <TextField label="Target type (optional)" name="target_type" autoComplete="off" />
        <TextField label="Correlation id (optional)" name="correlation_id" autoComplete="off" />
      </div>
    </Shell>
  );
}

export { ESCALATION_TARGETS };
