"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { OpsAction } from "@/components/admin/operations/ops-action";
import { StepUpCancelledError, useStepUp } from "@/components/auth/step-up";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { TextAreaField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { discoveryApi, type DiscoveryApi } from "@/lib/discovery/api";
import { WEIGHT_FIELDS, type Weights } from "@/lib/discovery/admin";
import { describeOpsError } from "@/lib/operations/errors";

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const len = (s: string) => [...s].length;
const NOTE = { kind: "textarea", name: "note", label: "Note (staff only)", min: 3, max: 1000 } as const;

/** Local datetime input value (YYYY-MM-DDTHH:mm) for a date. */
function localInput(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

/**
 * Request a featured placement (maker: discovery.feature.request + step-up). A second person approves it; the campaign
 * must be promotable now (the API checks; it re-checks at approval and every time the list is served).
 */
export function PlacementRequestForm({ initialCampaignId = "", api = discoveryApi }: { initialCampaignId?: string; api?: DiscoveryApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [notice, setNotice] = useState<string | null>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  const errorRef = useRef<HTMLDivElement>(null);
  const [defaults] = useState(() => {
    const now = new Date();
    return { start: localInput(now), end: localInput(new Date(now.getTime() + 7 * 24 * 3600 * 1000)) };
  });
  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const f = new FormData(form);
    const campaignId = String(f.get("campaign_id") ?? "").trim();
    const positionRaw = String(f.get("position") ?? "").trim();
    const reason = String(f.get("reason") ?? "").trim();
    const start = new Date(String(f.get("starts_at") ?? ""));
    const end = new Date(String(f.get("ends_at") ?? ""));
    const errs: Record<string, string> = {};
    if (!UUID.test(campaignId)) errs.campaign_id = "Paste the campaign's id (from its review page address).";
    const position = /^\d{1,3}$/.test(positionRaw) ? Number.parseInt(positionRaw, 10) : NaN;
    if (!(position >= 1 && position <= 100)) errs.position = "Choose a position from 1 to 100.";
    if (len(reason) < 10 || len(reason) > 500) errs.reason = "Write 10 to 500 characters explaining why.";
    if (Number.isNaN(start.getTime())) errs.starts_at = "Choose a start.";
    if (Number.isNaN(end.getTime()) || end <= start || end.getTime() - start.getTime() > 31 * 24 * 3600 * 1000) errs.ends_at = "End after the start, within 31 days.";
    setFieldErrors(errs);
    setNotice(null);
    if (Object.keys(errs).length > 0) {
      setError("Please correct the errors below.");
      requestAnimationFrame(() => errorRef.current?.focus());
      return;
    }
    setBusy(true);
    setError(null);
    const key = crypto.randomUUID(); // one key per submission: a retry after step-up reuses it
    try {
      await withStepUp(() => api.requestPlacement({ campaign_id: campaignId, position, reason, starts_at: start.toISOString(), ends_at: end.toISOString() }, key));
      form.reset();
      setNotice("Placement requested. A second team member must approve it before it is shown.");
      router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) {
        const d = describeOpsError(err, "The placement could not be requested.");
        if (d.field) setFieldErrors({ [d.field]: d.message });
        setError(d.message);
        requestAnimationFrame(() => errorRef.current?.focus());
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <form noValidate onSubmit={submit} className="space-y-4">
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      <FormError ref={errorRef} message={error} />
      <TextField label="Campaign id" name="campaign_id" defaultValue={initialCampaignId} autoComplete="off" spellCheck={false} error={fieldErrors.campaign_id} />
      <TextField label="Position (1–100)" name="position" inputMode="numeric" defaultValue="1" error={fieldErrors.position} />
      <div className="grid gap-4 sm:grid-cols-2">
        <TextField label="Shown from" name="starts_at" type="datetime-local" defaultValue={defaults.start} error={fieldErrors.starts_at} />
        <TextField label="Shown until" name="ends_at" type="datetime-local" defaultValue={defaults.end} error={fieldErrors.ends_at} />
      </div>
      <TextAreaField label="Why this campaign (staff only, 10–500 characters)" name="reason" required maxLength={500} error={fieldErrors.reason} />
      <SubmitButton busy={busy} busyLabel="Working…">
        Request placement
      </SubmitButton>
    </form>
  );
}

/** Decisions on a placement; the API refuses anything the caller may not do (permission, state, separation of duties). */
export function PlacementActions({ id, status, api = discoveryApi }: { id: string; status: string; api?: DiscoveryApi }) {
  return (
    <div className="flex flex-wrap gap-2">
      <OpsAction button="Approve" variant="primary" title="Approve this placement?" description="The campaign will be featured inside the requested window while it stays active and unrestricted. You cannot approve your own request."
        confirm="Confirm: Approve" notice="Placement approved." fields={[NOTE]} available={status === "REQUESTED"} run={(b) => api.decidePlacement(id, "approve", String(b.note))} />
      <OpsAction button="Reject" title="Reject this placement?" description="The request is closed; nothing is shown." confirm="Confirm: Reject" notice="Placement rejected."
        fields={[NOTE]} available={status === "REQUESTED"} run={(b) => api.decidePlacement(id, "reject", String(b.note))} />
      <OpsAction button="Withdraw" title="Withdraw your request?" description="Only the person who requested it can withdraw it." confirm="Confirm: Withdraw" notice="Request withdrawn."
        fields={[NOTE]} available={status === "REQUESTED"} run={(b) => api.decidePlacement(id, "cancel", String(b.note))} />
      <OpsAction button="Remove" title="Remove this placement now?" description="It stops being shown at once. The campaign itself is not changed." confirm="Confirm: Remove" notice="Placement removed."
        fields={[{ ...NOTE, label: "Reason (staff only)", max: 500 }]} available={status === "APPROVED"} run={(b) => api.decidePlacement(id, "remove", String(b.note))} />
    </div>
  );
}

/** Propose a ranking policy version (maker: discovery.ranking.request + step-up). Bounded integer weights only. */
export function RankingProposalForm({ current, api = discoveryApi }: { current: Weights; api?: DiscoveryApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [notice, setNotice] = useState<string | null>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  const errorRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const f = new FormData(event.currentTarget);
    const errs: Record<string, string> = {};
    const weights: Record<string, number> = {};
    for (const w of WEIGHT_FIELDS) {
      const raw = String(f.get(w.key) ?? "").trim();
      const n = /^\d{1,4}$/.test(raw) ? Number.parseInt(raw, 10) : NaN;
      if (!(n >= w.min && n <= w.max)) errs[w.key] = `Enter a whole number from ${w.min} to ${w.max}.`;
      weights[w.key] = n;
    }
    const reason = String(f.get("reason") ?? "").trim();
    if (len(reason) < 10 || len(reason) > 1000) errs.reason = "Write 10 to 1,000 characters explaining the change.";
    setFieldErrors(errs);
    setNotice(null);
    if (Object.keys(errs).length > 0) {
      setError("Please correct the errors below.");
      requestAnimationFrame(() => errorRef.current?.focus());
      return;
    }
    setBusy(true);
    setError(null);
    const key = crypto.randomUUID();
    try {
      await withStepUp(() => api.proposeRanking(weights, reason, key));
      setNotice("Proposal submitted. A second team member must approve it before it is used.");
      router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) {
        setError(describeOpsError(err, "The proposal could not be submitted.").message);
        requestAnimationFrame(() => errorRef.current?.focus());
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <form noValidate onSubmit={submit} className="space-y-4">
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      <FormError ref={errorRef} message={error} />
      <div className="grid gap-4 sm:grid-cols-2">
        {WEIGHT_FIELDS.map((w) => (
          <TextField key={w.key} label={w.label} name={w.key} inputMode="numeric" defaultValue={String(current[w.key])} hint={w.hint} error={fieldErrors[w.key]} />
        ))}
      </div>
      <TextAreaField label="Why (staff only)" name="reason" required maxLength={1000} error={fieldErrors.reason} />
      <SubmitButton busy={busy} busyLabel="Working…">
        Submit proposal
      </SubmitButton>
    </form>
  );
}

export function RankingActions({ id, status, api = discoveryApi }: { id: string; status: string; api?: DiscoveryApi }) {
  return (
    <div className="flex flex-wrap gap-2">
      <OpsAction button="Approve" variant="primary" title="Approve and activate this version?" description="It replaces the weights in force at the next trending computation. You cannot approve your own proposal."
        confirm="Confirm: Approve" notice="Version approved and active." fields={[NOTE]} available={status === "SUBMITTED"} run={(b) => api.decideRanking(id, "approve", String(b.note))} />
      <OpsAction button="Reject" title="Reject this version?" description="The weights in force stay." confirm="Confirm: Reject" notice="Version rejected." fields={[NOTE]}
        available={status === "SUBMITTED"} run={(b) => api.decideRanking(id, "reject", String(b.note))} />
      <OpsAction button="Withdraw" title="Withdraw your proposal?" description="Only the proposer can withdraw it." confirm="Confirm: Withdraw" notice="Proposal withdrawn." fields={[NOTE]}
        available={status === "SUBMITTED"} run={(b) => api.decideRanking(id, "withdraw", String(b.note))} />
    </div>
  );
}

/** Index and referral operations (discovery.operate + step-up). */
export function DiscoveryOperations({ api = discoveryApi }: { api?: DiscoveryApi }) {
  return (
    <div className="flex flex-wrap gap-2">
      <OpsAction button="Reconcile the search index now" title="Reconcile the search index?" description="Compares up to 2,000 campaigns with their current eligibility and fixes any difference. The periodic job covers the rest."
        confirm="Confirm: Reconcile" notice="Reconciliation finished." run={() => api.reconcile()} />
      <OpsAction button="Revoke a referral code" title="Revoke a referral code?" description="Visits with this code stop counting as referrals. Use it for codes spread as spam."
        confirm="Confirm: Revoke" notice="Code revoked." fields={[{ kind: "text", name: "code", label: "Referral code (12 characters)" }, { ...NOTE, label: "Reason (staff only)" }]}
        run={(b) => api.revokeReferral(String(b.code).toLowerCase(), String(b.note))} />
    </div>
  );
}
