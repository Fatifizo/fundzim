"use client";

import Link from "next/link";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { formatDateTime } from "@/components/account/format";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SubmitButton } from "@/components/forms/submit-button";
import { Alert } from "@/components/ui/alert";
import { ButtonLink } from "@/components/ui/button";
import { campaignApi, type CampaignApi } from "@/lib/campaigns/api";
import { describeCampaignError } from "@/lib/campaigns/errors";
import type { AgeAttestation } from "@/lib/campaigns/types";

const BASIC_UNMET: Record<string, string> = {
  EMAIL_NOT_VERIFIED: "Confirm your email address.",
  PHONE_NOT_VERIFIED: "Confirm your phone number.",
  AGE_ATTESTATION_REQUIRED: "Declare that you are an adult.",
  AGE_NOT_ELIGIBLE: "Your verified date of birth does not meet the age requirement.",
  ACCOUNT_NOT_ACTIVE: "Your account is not active.",
  BASIC_POLICY_UNAVAILABLE: "Verification rules are temporarily unavailable. Try again later.",
};

/**
 * Age self-declaration (ADR-037 §1). It is a declaration, not proof: "verified age" only ever comes from an
 * approved identity verification. A change is recorded as a new declaration; nothing is overwritten.
 */
export function AgeAttestationForm({ initial, next, api = campaignApi }: { initial: AgeAttestation; next: string | null; api?: CampaignApi }) {
  const [current, setCurrent] = useState(initial);
  const [error, setError] = useState<string | null>(null);
  const [fieldError, setFieldError] = useState<string | undefined>();
  const [busy, setBusy] = useState(false);
  // A counter, so a second declaration in the same visit moves focus to the outcome again.
  const [done, setDone] = useState(0);
  const errorRef = useRef<HTMLDivElement>(null);
  const doneRef = useRef<HTMLDivElement>(null);
  const age = current.adult_age;

  useEffect(() => {
    if (done > 0) doneRef.current?.focus();
  }, [done]);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const choice = String(new FormData(event.currentTarget).get("outcome") ?? "");
    if (choice !== "ATTESTED" && choice !== "DECLINED") {
      setFieldError("Choose one of the options.");
      setError("Please correct the errors below.");
      requestAnimationFrame(() => document.getElementById("field-outcome")?.focus());
      return;
    }
    setBusy(true);
    setError(null);
    setFieldError(undefined);
    try {
      const result = await api.attestAge(choice, current.statement_version);
      setCurrent({ ...result, outcome: result.outcome ?? choice });
      setDone((n) => n + 1);
    } catch (err) {
      setError(describeCampaignError(err, "Your declaration could not be saved. Please try again.").message);
      requestAnimationFrame(() => errorRef.current?.focus());
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-6">
      <Alert tone="info" title="This is a self-declaration, not proof of age">
        <p>
          You confirm your own age. FundZim records your answer, the wording you agreed to and the time. It does not check documents at this step; your age is only verified if you later complete identity verification. Declaring an age that is not true may lead to your account being closed.
        </p>
      </Alert>
      {current.outcome ? (
        <p className="text-ink-700">
          Your current declaration: <span className="font-semibold">{current.outcome === "ATTESTED" ? `I am at least ${age} years old` : `I am not ${age} or older`}</span>
          {current.attested_at ? ` (recorded ${formatDateTime(current.attested_at)})` : ""}.
        </p>
      ) : (
        <p className="text-ink-700">You have not made a declaration yet.</p>
      )}
      {done ? (
        <FormStatus ref={doneRef} title="Your declaration was recorded">
          {current.outcome === "ATTESTED" ? (
            <>
              <p>
                {current.level === "BASIC_VERIFIED" || current.level === "IDENTITY_VERIFIED" || current.level === "PAYOUT_VERIFIED"
                  ? "Thank you. Basic verification is complete."
                  : "Thank you. Basic verification also needs a confirmed email address and phone number."}{" "}
                <Link href="/dashboard/verification" className="font-semibold text-brand-700 underline">
                  See your verification status
                </Link>
                .
              </p>
              {current.basic_unmet.length > 0 ? (
                <ul className="mt-2 list-disc space-y-1 pl-6">
                  {current.basic_unmet.map((code) => (
                    <li key={code}>{BASIC_UNMET[code] ?? "Something else is still needed for basic verification."}</li>
                  ))}
                </ul>
              ) : null}
              {current.basic_unmet.some((c) => c === "EMAIL_NOT_VERIFIED" || c === "PHONE_NOT_VERIFIED") ? (
                <p className="mt-2">
                  <Link href="/settings/security" className="font-semibold text-brand-700 underline">
                    Confirm your email and phone in security settings
                  </Link>
                </p>
              ) : null}
            </>
          ) : (
            <p>You can still donate in future, but you can&apos;t raise funds on FundZim. A parent or guardian can raise funds on your behalf.</p>
          )}
        </FormStatus>
      ) : null}
      {done && next ? (
        <ButtonLink href={next} variant="primary">
          Continue where you were
        </ButtonLink>
      ) : null}
      <form noValidate onSubmit={onSubmit} className="space-y-5">
        <FormError ref={errorRef} message={error} />
        <fieldset id="field-outcome" tabIndex={-1} aria-invalid={fieldError ? true : undefined} aria-describedby={fieldError ? "outcome-error" : undefined} className="space-y-2 focus:outline-none">
          <legend className="font-semibold text-ink-900">Your declaration</legend>
          <label className="flex min-h-11 items-start gap-3 rounded-xl border-2 border-line p-3 has-[:checked]:border-brand-700 has-[:checked]:bg-brand-50">
            <input type="radio" name="outcome" value="ATTESTED" defaultChecked={current.outcome === "ATTESTED"} className="mt-1 size-5 shrink-0 accent-brand-700" />
            <span>I declare that I am at least {age} years old.</span>
          </label>
          <label className="flex min-h-11 items-start gap-3 rounded-xl border-2 border-line p-3 has-[:checked]:border-brand-700 has-[:checked]:bg-brand-50">
            <input type="radio" name="outcome" value="DECLINED" defaultChecked={current.outcome === "DECLINED"} className="mt-1 size-5 shrink-0 accent-brand-700" />
            <span>I am not {age} or older.</span>
          </label>
          {fieldError ? (
            <p id="outcome-error" className="text-sm font-semibold text-danger-700">
              <span aria-hidden="true">Error: </span>
              {fieldError}
            </p>
          ) : null}
        </fieldset>
        <SubmitButton busy={busy} busyLabel="Saving…">
          Save my declaration
        </SubmitButton>
      </form>
    </div>
  );
}
