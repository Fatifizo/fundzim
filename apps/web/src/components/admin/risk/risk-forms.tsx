"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";

import { StepUpCancelledError, useStepUp } from "@/components/auth/step-up";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SelectField, TextAreaField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { Button } from "@/components/ui/button";
import { humanise } from "@/lib/payments/labels";
import { riskApi, type RiskApi, type RuleVersionBody } from "@/lib/risk/api";
import { describeRiskError } from "@/lib/risk/errors";
import { readRecon, readSimulation } from "@/lib/risk/normalise";
import {
  AUTOMATED_HOLD_TYPES,
  CASE_TYPES,
  HOLD_SUBJECTS,
  HOLD_TYPES,
  PRIORITIES,
  RISK_CATEGORIES,
  RULE_ACTIONS,
  RULE_SEVERITIES,
  SCREENING_SCENARIOS,
  SCREENING_SUBJECTS,
  STAFF_EVIDENCE_TYPES,
  SUBJECT_TYPES,
  type ReconReport,
  type RuleVersion,
  type Simulation,
} from "@/lib/risk/types";

const opts = (values: readonly string[]) => values.map((v) => ({ value: v, label: humanise(v) }));
const RULE_CODE = /^[A-Z][A-Z0-9_]{2,63}$/;
const REASON_CODE = /^[A-Z][A-Z0-9_]{2,63}$/;
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const len = (s: string) => [...s].length;

/** Shared submit plumbing: one Idempotency-Key per distinct body, step-up retry, focus on notice or error. */
function useFormSubmit() {
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
  async function run<T>(body: unknown, call: (key: string) => Promise<T>, done: (r: T) => void, refresh = true) {
    setFieldErrors({});
    setBusy(true);
    setError(null);
    setNotice(null);
    const raw = JSON.stringify(body);
    if (!keyRef.current || keyRef.current.body !== raw) keyRef.current = { body: raw, key: crypto.randomUUID() };
    const key = keyRef.current.key;
    try {
      const r = await withStepUp(() => call(key));
      keyRef.current = null;
      done(r);
      if (refresh) router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) fail(describeRiskError(err).message);
    } finally {
      setBusy(false);
    }
  }
  const status = (
    <>
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      <FormError ref={errorRef} message={error} />
    </>
  );
  return { router, busy, fieldErrors, run, fail, setNotice, status };
}

function FormShell({ label, children, onSubmit }: { label: string; children: ReactNode; onSubmit: (e: FormEvent<HTMLFormElement>) => void }) {
  return (
    <form noValidate onSubmit={onSubmit} className="space-y-5" aria-label={label}>
      {children}
    </form>
  );
}

const value = (f: FormData, k: string) => String(f.get(k) ?? "").trim();
const idOf = (r: unknown) => {
  const id = (r as { id?: unknown })?.id;
  return typeof id === "string" && UUID.test(id) ? id : null;
};

/** Reads and validates the rule version fields; errors keyed by field name. */
function readVersion(d: FormData): { body: RuleVersionBody | null; errs: Record<string, string> } {
  const errs: Record<string, string> = {};
  let definition: unknown = null;
  try {
    definition = JSON.parse(value(d, "definition"));
    if (typeof definition !== "object" || definition === null || Array.isArray(definition)) throw new Error("not an object");
  } catch {
    errs.definition = "Paste a JSON object such as {\"when\": {…}}.";
  }
  const contribution = Number.parseInt(value(d, "contribution"), 10);
  if (!/^\d{1,3}$/.test(value(d, "contribution")) || contribution > 100) errs.contribution = "Use a whole number from 0 to 100.";
  const action = value(d, "recommended_action");
  let holdType: string | null = null;
  let holdSeconds: number | null = null;
  if (action === "TEMPORARY_HOLD") {
    holdType = value(d, "hold_type");
    const hours = value(d, "hold_hours");
    if (!/^\d{1,3}$/.test(hours) || Number.parseInt(hours, 10) < 1 || Number.parseInt(hours, 10) > 168) errs.hold_hours = "Use 1 to 168 hours (at most 7 days).";
    else holdSeconds = Number.parseInt(hours, 10) * 3600;
  }
  if (Object.keys(errs).length) return { body: null, errs };
  return { body: { definition, contribution, severity: value(d, "severity"), recommended_action: action, hold_type: holdType, hold_duration_seconds: holdSeconds,
    case_priority: value(d, "case_priority") }, errs };
}

function VersionFields({ initial, errors }: { initial?: RuleVersion; errors: Record<string, string> }) {
  const [action, setAction] = useState(initial?.recommended_action ?? "REVIEW_REQUIRED");
  return (
    <>
      <TextAreaField
        label="Definition (JSON)"
        name="definition"
        rows={8}
        required
        defaultValue={initial?.definition ?? '{\n  "when": {\n    "signal_count": {"signal_types": ["CAMPAIGN_REPORTED"], "window_seconds": 3600, "op": ">=", "value": 2}\n  }\n}'}
        hint="Declarative conditions only (all, any, signal_count, distinct_count, fact_compare, sequence, state) over catalogued signals. No code."
        error={errors.definition}
      />
      <TextField label="Contribution (0–100)" name="contribution" inputMode="numeric" required autoComplete="off" defaultValue={String(initial?.contribution ?? 20)} error={errors.contribution} />
      <SelectField label="Severity" name="severity" options={opts(RULE_SEVERITIES)} defaultValue={initial?.severity ?? "MEDIUM"} />
      <SelectField label="Recommended action" name="recommended_action" options={opts(RULE_ACTIONS)} value={action} onChange={(e) => setAction(e.target.value)} hint="Only a temporary withdrawal or destination hold can be automated; every other action opens a case for people." />
      {action === "TEMPORARY_HOLD" ? (
        <>
          <SelectField label="Hold type" name="hold_type" options={opts(AUTOMATED_HOLD_TYPES)} defaultValue={initial?.hold_type ?? "WITHDRAWAL_HOLD"} />
          <TextField label="Hold duration (hours)" name="hold_hours" inputMode="numeric" autoComplete="off" defaultValue={initial?.hold_duration_seconds ? String(Math.round(initial.hold_duration_seconds / 3600)) : "24"} hint="1 to 168 hours (at most 7 days)." error={errors.hold_hours} />
        </>
      ) : null}
      <SelectField label="Case priority" name="case_priority" options={opts(PRIORITIES)} defaultValue={initial?.case_priority ?? "P3"} />
    </>
  );
}

/** Create a rule with its first draft version (risk.rule.request). */
export function CreateRuleForm({ api = riskApi }: { api?: RiskApi }) {
  const f = useFormSubmit();
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const d = new FormData(form);
    const v = readVersion(d);
    const errs: Record<string, string> = { ...v.errs };
    const code = value(d, "code").toUpperCase();
    const name = value(d, "name");
    if (!RULE_CODE.test(code)) errs.code = "Use 3–64 capital letters, digits or underscores, starting with a letter.";
    if (len(name) < 3 || len(name) > 120) errs.name = "Write 3 to 120 characters.";
    else if (/fraud/i.test(name)) errs.name = "Describe the risk pattern, not a conclusion about a person.";
    if (Object.keys(errs).length || !v.body) return f.fail("Please correct the errors below.", errs);
    const body = { code, name, description: value(d, "description"), subject_type: value(d, "subject_type"), category: value(d, "category"), version: v.body };
    await f.run(body, (key) => api.createRule(body, key), (r) => {
      const id = idOf(r);
      if (id) f.router.push(`/admin/risk/rules/${encodeURIComponent(id)}`);
      else f.setNotice("Rule created.");
    });
  }
  return (
    <FormShell label="Create a risk rule" onSubmit={submit}>
      {f.status}
      <TextField label="Code" name="code" required autoComplete="off" hint="For example REPEATED_DESTINATION_CHANGES" error={f.fieldErrors.code} />
      <TextField label="Name" name="name" required autoComplete="off" error={f.fieldErrors.name} />
      <TextAreaField label="Description (optional)" name="description" maxLength={2000} />
      <SelectField label="Subject type" name="subject_type" options={opts(SUBJECT_TYPES)} />
      <SelectField label="Category" name="category" options={opts(RISK_CATEGORIES)} />
      <VersionFields errors={f.fieldErrors} />
      <SubmitButton busy={f.busy} busyLabel="Creating…">
        Create rule
      </SubmitButton>
    </FormShell>
  );
}

/** Draft a new version of a rule, or edit an existing DRAFT (definition and attributes are editable only in DRAFT). */
export function RuleVersionForm({ ruleId, draft, api = riskApi }: { ruleId: string; draft?: RuleVersion; api?: RiskApi }) {
  const f = useFormSubmit();
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const v = readVersion(new FormData(event.currentTarget));
    if (!v.body) return f.fail("Please correct the errors below.", v.errs);
    const body = v.body;
    if (draft) await f.run(body, () => api.updateDraft(draft.id, body), () => f.setNotice("Draft saved."));
    else await f.run(body, (key) => api.createRuleVersion(ruleId, body, key), () => f.setNotice("Draft version created."));
  }
  return (
    <FormShell label={draft ? "Edit the draft version" : "Draft a rule version"} onSubmit={submit}>
      {f.status}
      <VersionFields initial={draft} errors={f.fieldErrors} />
      <SubmitButton busy={f.busy} busyLabel="Saving…">
        {draft ? "Save draft" : "Create draft"}
      </SubmitButton>
    </FormShell>
  );
}

/** Evaluate one version against a subject's recorded signals: nothing is stored and no action is taken. */
export function SimulateForm({ versionId, api = riskApi }: { versionId: string; api?: RiskApi }) {
  const f = useFormSubmit();
  const [result, setResult] = useState<Simulation | null>(null);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const subject = value(new FormData(event.currentTarget), "subject_id");
    if (!UUID.test(subject)) return f.fail("Please correct the errors below.", { subject_id: "Paste the subject id." });
    setResult(null);
    await f.run({ subject }, () => api.simulate(versionId, subject), (r) => setResult(readSimulation(r)), false);
  }
  return (
    <FormShell label="Simulate this version" onSubmit={submit}>
      {f.status}
      <TextField label="Subject id" name="subject_id" required autoComplete="off" error={f.fieldErrors.subject_id} />
      <SubmitButton busy={f.busy} busyLabel="Simulating…">
        Run simulation
      </SubmitButton>
      {result ? (
        <div role="status" className="space-y-2 rounded-card border border-line bg-surface p-4">
          <p className="font-semibold text-ink-900">Simulation – nothing stored</p>
          <p className="text-ink-800">
            Score {result.score} / 100 · {humanise(result.classification)} · recommended action {humanise(result.recommended_action)} · confidence {result.confidence}
          </p>
          <ul className="list-disc pl-5 text-sm text-ink-800">
            {result.results.map((o) => (
              <li key={o.rule_version_id + o.rule_code}>
                {o.fired ? "Fired" : "Did not fire"}: {o.conditions.join("; ") || "no conditions met"}
              </li>
            ))}
          </ul>
          <p className="text-sm text-ink-600">{result.notice}</p>
        </div>
      ) : null}
    </FormShell>
  );
}

/** Open (or link to) a case manually (risk.case.work). */
export function OpenCaseForm({ api = riskApi }: { api?: RiskApi }) {
  const f = useFormSubmit();
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const d = new FormData(event.currentTarget);
    const body = { case_type: value(d, "case_type"), subject_type: value(d, "subject_type"), subject_id: value(d, "subject_id"), priority: value(d, "priority"), reason: value(d, "reason") };
    const errs: Record<string, string> = {};
    if (!UUID.test(body.subject_id)) errs.subject_id = "Paste the subject id.";
    if (len(body.reason) < 5 || len(body.reason) > 2000) errs.reason = "Write 5 to 2,000 characters.";
    if (Object.keys(errs).length) return f.fail("Please correct the errors below.", errs);
    await f.run(body, (key) => api.openCase(body, key), (r) => {
      const id = idOf(r);
      if (id) f.router.push(`/admin/risk/cases/${encodeURIComponent(id)}`);
      else f.setNotice("Case opened.");
    });
  }
  return (
    <FormShell label="Open a risk case" onSubmit={submit}>
      {f.status}
      <SelectField label="Case type" name="case_type" options={opts(CASE_TYPES)} />
      <SelectField label="Subject type" name="subject_type" options={opts(SUBJECT_TYPES)} />
      <TextField label="Subject id" name="subject_id" required autoComplete="off" error={f.fieldErrors.subject_id} />
      <SelectField label="Priority" name="priority" options={opts(PRIORITIES)} defaultValue="P3" />
      <TextAreaField label="Reason (staff only)" name="reason" required maxLength={2000} error={f.fieldErrors.reason} />
      <SubmitButton busy={f.busy} busyLabel="Opening…">
        Open case
      </SubmitButton>
    </FormShell>
  );
}

/** Add a note or an evidence reference (immutable and hashed; a correction supersedes, never overwrites). */
export function EvidenceForm({ caseId, existing, api = riskApi }: { caseId: string; existing: Array<{ id: string; label: string }>; api?: RiskApi }) {
  const f = useFormSubmit();
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const d = new FormData(form);
    const body = { evidence_type: value(d, "evidence_type"), note: value(d, "note"), ...(value(d, "reference_type") ? { reference_type: value(d, "reference_type") } : {}),
      ...(value(d, "reference_id") ? { reference_id: value(d, "reference_id") } : {}), ...(value(d, "supersedes_id") ? { supersedes_id: value(d, "supersedes_id") } : {}) };
    const errs: Record<string, string> = {};
    if (len(body.note) < 3 || len(body.note) > 4000) errs.note = "Write 3 to 4,000 characters.";
    if (body.evidence_type !== "STAFF_NOTE" && (!body.reference_type || !body.reference_id)) errs.reference_id = "A reference needs its type and id.";
    if (Object.keys(errs).length) return f.fail("Please correct the errors below.", errs);
    await f.run(body, (key) => api.addEvidence(caseId, body, key), () => {
      form.reset();
      f.setNotice("Evidence added.");
    });
  }
  return (
    <FormShell label="Add evidence" onSubmit={submit}>
      {f.status}
      <SelectField label="Evidence type" name="evidence_type" options={opts(STAFF_EVIDENCE_TYPES)} />
      <TextAreaField label="Note (staff only)" name="note" required maxLength={4000} hint="Never paste identity documents, full account numbers or secrets." error={f.fieldErrors.note} />
      <TextField label="Reference type (for references)" name="reference_type" autoComplete="off" hint="For example PAYOUT_REQUEST" />
      <TextField label="Reference id (for references)" name="reference_id" autoComplete="off" error={f.fieldErrors.reference_id} />
      {existing.length ? (
        <SelectField label="Corrects earlier evidence (optional)" name="supersedes_id" placeholderOption="None" options={existing.map((e) => ({ value: e.id, label: e.label }))} />
      ) : null}
      <SubmitButton busy={f.busy} busyLabel="Adding…">
        Add evidence
      </SubmitButton>
    </FormShell>
  );
}

/** Place a hold (risk.hold.create). Campaign, donation and account-review holds need a second person to activate. */
export function CreateHoldForm({ api = riskApi }: { api?: RiskApi }) {
  const f = useFormSubmit();
  const [holdType, setHoldType] = useState<(typeof HOLD_TYPES)[number]>("WITHDRAWAL_HOLD");
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const d = new FormData(form);
    const body = { hold_type: value(d, "hold_type"), subject_type: value(d, "subject_type"), subject_id: value(d, "subject_id"), reason_code: value(d, "reason_code").toUpperCase(),
      description: value(d, "description"), ...(value(d, "case_id") ? { case_id: value(d, "case_id") } : {}) };
    const errs: Record<string, string> = {};
    if (!UUID.test(body.subject_id)) errs.subject_id = "Paste the subject id.";
    if (!REASON_CODE.test(body.reason_code)) errs.reason_code = "Use 3–64 capital letters, digits or underscores.";
    if (len(body.description) < 5 || len(body.description) > 2000) errs.description = "Write 5 to 2,000 characters.";
    if (body.case_id && !UUID.test(body.case_id)) errs.case_id = "Paste the case id.";
    if (Object.keys(errs).length) return f.fail("Please correct the errors below.", errs);
    await f.run(body, (key) => api.createHold(body, key), () => {
      form.reset();
      f.setNotice("Hold placed.");
    });
  }
  return (
    <FormShell label="Place a hold" onSubmit={submit}>
      {f.status}
      <SelectField label="Hold type" name="hold_type" options={opts(HOLD_TYPES)} value={holdType} onChange={(e) => setHoldType(e.target.value as (typeof HOLD_TYPES)[number])} />
      <SelectField key={holdType} label="Subject type" name="subject_type" options={opts(HOLD_SUBJECTS[holdType])} />
      <TextField label="Subject id" name="subject_id" required autoComplete="off" error={f.fieldErrors.subject_id} />
      <TextField label="Reason code" name="reason_code" required autoComplete="off" hint="For example MANUAL_REVIEW" error={f.fieldErrors.reason_code} />
      <TextAreaField label="Description (staff only)" name="description" required maxLength={2000} error={f.fieldErrors.description} />
      <TextField label="Case id (optional)" name="case_id" autoComplete="off" error={f.fieldErrors.case_id} />
      <SubmitButton busy={f.busy} busyLabel="Placing…">
        Place hold
      </SubmitButton>
    </FormShell>
  );
}

/** Request a TEST screening with a simulator scenario (no identity data is sent anywhere). */
export function ScreeningRequestForm({ api = riskApi }: { api?: RiskApi }) {
  const f = useFormSubmit();
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const d = new FormData(event.currentTarget);
    const body = { subject_type: value(d, "subject_type"), subject_id: value(d, "subject_id"), scenario: value(d, "scenario") };
    if (!UUID.test(body.subject_id)) return f.fail("Please correct the errors below.", { subject_id: "Paste the subject id." });
    await f.run(body, (key) => api.requestScreening(body, key), (r) => {
      const id = idOf(r);
      if (id) f.router.push(`/admin/risk/screening/${encodeURIComponent(id)}`);
      else f.setNotice("Screening requested.");
    });
  }
  return (
    <FormShell label="Request a TEST screening" onSubmit={submit}>
      {f.status}
      <SelectField label="Subject type" name="subject_type" options={opts(SCREENING_SUBJECTS)} />
      <TextField label="Subject id" name="subject_id" required autoComplete="off" error={f.fieldErrors.subject_id} />
      <SelectField label="Simulator scenario" name="scenario" options={opts(SCREENING_SCENARIOS)} hint="TEST simulator only: results are synthetic and labelled TEST_ONLY." />
      <SubmitButton busy={f.busy} busyLabel="Requesting…">
        Request screening
      </SubmitButton>
    </FormShell>
  );
}

/** Runs risk reconciliation and shows its checks (findings also become monitoring events). */
export function ReconRunPanel({ api = riskApi }: { api?: RiskApi }) {
  const f = useFormSubmit();
  const [report, setReport] = useState<ReconReport | null>(null);
  return (
    <div className="space-y-3">
      {f.status}
      <Button variant="outline" disabled={f.busy} onClick={() => f.run({ run: Date.now() }, () => api.runReconciliation(), (r) => setReport(readRecon(r)))}>
        {f.busy ? "Running…" : "Run reconciliation"}
      </Button>
      {report ? (
        <div role="status" className="space-y-2">
          <p className="font-semibold text-ink-900">Reconciliation finished. It never edits evidence, releases holds or closes cases.</p>
          <ul className="list-disc pl-5 text-ink-800">
            {report.checks.map((c) => (
              <li key={c.code}>
                {humanise(c.code)}: {c.status === "PASS" ? "pass" : `${c.findings} finding${c.findings === 1 ? "" : "s"}`}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </div>
  );
}
