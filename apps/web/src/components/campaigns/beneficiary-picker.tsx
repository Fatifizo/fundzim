"use client";

import Link from "next/link";
import { useId, useRef, useState, type FormEvent } from "react";

import { FormError, FormStatus } from "@/components/forms/form-error";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { Badge } from "@/components/ui/badge";
import { CaseStatusBadge } from "@/components/verification/status";
import { campaignApi, type CampaignApi } from "@/lib/campaigns/api";
import { describeCampaignError } from "@/lib/campaigns/errors";
import type { CampaignBeneficiary, Disclosure, EligibilityReason } from "@/lib/campaigns/types";
import { humanise } from "@/lib/campaigns/labels";
import type { Beneficiary } from "@/lib/verification/types";

import { EligibilityList } from "./eligibility-list";

/**
 * Choose the campaign's beneficiary from the owner's existing beneficiaries (GET /beneficiaries) — or go to
 * the beneficiary verification area to register one. Disclosure controls what the public page shows; the
 * default is to keep details private.
 */
export function BeneficiaryPicker({
  campaignId,
  organisationId,
  beneficiaries,
  current,
  editable,
  onLinked,
  api = campaignApi,
}: {
  campaignId: string;
  organisationId: string | null;
  beneficiaries: Beneficiary[];
  current: CampaignBeneficiary | null;
  editable: boolean;
  onLinked?: (b: CampaignBeneficiary) => void;
  api?: CampaignApi;
}) {
  const eligible = beneficiaries.filter((b) => (organisationId ? b.owner.type === "ORGANISATION" && b.owner.id === organisationId : b.owner.type === "USER"));
  const [selected, setSelected] = useState(current?.beneficiary_id ?? "");
  const [disclosure, setDisclosure] = useState<Disclosure>(current?.disclosure ?? "NONE");
  const [consent, setConsent] = useState(current?.consent_declared ?? false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [reasons, setReasons] = useState<EligibilityReason[]>([]);
  const [saved, setSaved] = useState(false);
  const errorRef = useRef<HTMLDivElement>(null);
  const savedRef = useRef<HTMLDivElement>(null);
  const formRef = useRef<HTMLFormElement>(null);
  const id = useId();
  const changing = !!current && current.beneficiary_id !== selected;

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const reason = String(new FormData(event.currentTarget).get("reason") ?? "").trim();
    const errors: Record<string, string> = {};
    if (!selected) errors.beneficiary = "Choose a beneficiary.";
    if (!consent) errors.consent = "Confirm the beneficiary's consent (or that you are the beneficiary).";
    if (changing && reason.length < 3) errors.reason = "Say briefly why you are changing the beneficiary.";
    setFieldErrors(errors);
    setReasons([]);
    setSaved(false);
    if (Object.keys(errors).length > 0) {
      setError("Please correct the errors below.");
      requestAnimationFrame(() => (formRef.current?.querySelector<HTMLElement>('[aria-invalid="true"]') ?? errorRef.current)?.focus());
      return;
    }
    setError(null);
    setBusy(true);
    try {
      await api.linkBeneficiary(campaignId, { beneficiary_id: selected, disclosure, consent_declared: true, ...(changing ? { reason } : {}) });
      const b = eligible.find((x) => x.id === selected);
      onLinked?.({ beneficiary_id: selected, display_name: b?.display_name ?? null, disclosure, consent_declared: true, verification_status: b?.verification.status ?? null });
      setSaved(true);
      requestAnimationFrame(() => savedRef.current?.focus());
    } catch (err) {
      const d = describeCampaignError(err, "The beneficiary could not be saved. Please try again.");
      setReasons(d.reasons);
      setError(d.message);
      requestAnimationFrame(() => errorRef.current?.focus());
    } finally {
      setBusy(false);
    }
  }

  return (
    <form ref={formRef} noValidate onSubmit={onSubmit} className="space-y-6" aria-label="Beneficiary">
      <FormError ref={errorRef} message={error} />
      {reasons.length > 0 ? <EligibilityList reasons={reasons} allowed={false} allowedText="" context={{ campaignId, organisationId }} /> : null}
      {saved ? <FormStatus ref={savedRef} title="Beneficiary saved" /> : null}
      <fieldset id="field-beneficiary" tabIndex={-1} aria-invalid={fieldErrors.beneficiary ? true : undefined} aria-describedby={fieldErrors.beneficiary ? `${id}-ben-error` : `${id}-ben-hint`} className="space-y-3 focus:outline-none" disabled={!editable}>
        <legend className="font-semibold text-ink-900">Who will benefit?</legend>
        <p id={`${id}-ben-hint`} className="text-sm text-ink-600">
          Choose from the beneficiaries you have registered{organisationId ? " for this organisation" : ""}. A beneficiary must be verified before the campaign can be approved.
        </p>
        {eligible.length === 0 ? (
          <p className="text-ink-700">You have no beneficiaries yet.</p>
        ) : (
          <div className="space-y-2">
            {eligible.map((b) => (
              <label key={b.id} className="flex min-h-11 cursor-pointer items-start gap-3 rounded-xl border-2 border-line p-3 has-[:checked]:border-brand-700 has-[:checked]:bg-brand-50">
                <input type="radio" name="beneficiary_id" value={b.id} checked={selected === b.id} onChange={() => setSelected(b.id)} className="mt-1 size-5 shrink-0 accent-brand-700" />
                <span className="space-y-1">
                  <span className="block font-semibold break-words text-ink-900">{b.display_name}</span>
                  <span className="flex flex-wrap items-center gap-2 text-sm text-ink-700">
                    {humanise(b.beneficiary_type)} <CaseStatusBadge status={b.verification.status} />
                  </span>
                </span>
              </label>
            ))}
          </div>
        )}
        {fieldErrors.beneficiary ? (
          <p id={`${id}-ben-error`} className="text-sm font-semibold text-danger-700">
            <span aria-hidden="true">Error: </span>
            {fieldErrors.beneficiary}
          </p>
        ) : null}
        <p>
          <Link href="/dashboard/verification/beneficiaries" className="inline-flex min-h-11 items-center font-semibold text-brand-700 underline">
            Register a new beneficiary
          </Link>
          <span className="text-sm text-ink-600"> — then come back here to choose them.</span>
        </p>
      </fieldset>

      <fieldset className="space-y-2" disabled={!editable}>
        <legend className="font-semibold text-ink-900">What the public page shows about the beneficiary</legend>
        <label className="flex min-h-11 items-center gap-3 text-ink-900">
          <input type="radio" name="disclosure" value="NONE" checked={disclosure === "NONE"} onChange={() => setDisclosure("NONE")} className="size-5 accent-brand-700" />
          Keep their details private (recommended)
        </label>
        <label className="flex min-h-11 items-center gap-3 text-ink-900">
          <input type="radio" name="disclosure" value="DISPLAY_NAME" checked={disclosure === "DISPLAY_NAME"} onChange={() => setDisclosure("DISPLAY_NAME")} className="size-5 accent-brand-700" />
          Show their display name
        </label>
        <p className="text-sm text-ink-600">Never show a child&apos;s full name or health details unless it is necessary and you have consent.</p>
      </fieldset>

      <div className="space-y-1">
        <label className="flex items-start gap-3 text-ink-900">
          <input
            id="field-consent"
            type="checkbox"
            checked={consent}
            onChange={(e) => setConsent(e.target.checked)}
            disabled={!editable}
            aria-invalid={fieldErrors.consent ? true : undefined}
            aria-describedby={fieldErrors.consent ? `${id}-consent-error` : undefined}
            className="mt-1 size-6 shrink-0 accent-brand-700"
          />
          <span>I am the beneficiary, or the beneficiary (or their parent or guardian) has agreed to this fundraiser.</span>
        </label>
        {fieldErrors.consent ? (
          <p id={`${id}-consent-error`} className="pl-9 text-sm font-semibold text-danger-700">
            <span aria-hidden="true">Error: </span>
            {fieldErrors.consent}
          </p>
        ) : null}
      </div>

      {changing ? (
        <TextField
          id="field-reason"
          label="Why are you changing the beneficiary?"
          name="reason"
          maxLength={500}
          hint="A change to a published campaign's beneficiary is reviewed again before it is shown."
          error={fieldErrors.reason}
        />
      ) : null}

      {current ? (
        <p className="flex flex-wrap items-center gap-2 text-ink-700">
          Current beneficiary: <span className="font-semibold">{current.display_name ?? "Linked"}</span>
          <Badge tone="neutral">{current.disclosure === "NONE" ? "Details private" : "Name shown"}</Badge>
        </p>
      ) : null}

      {editable ? (
        <SubmitButton busy={busy} busyLabel="Saving…" disabled={eligible.length === 0}>
          Save beneficiary
        </SubmitButton>
      ) : (
        <p className="text-ink-700">The beneficiary can&apos;t be changed in the campaign&apos;s current status.</p>
      )}
    </form>
  );
}
