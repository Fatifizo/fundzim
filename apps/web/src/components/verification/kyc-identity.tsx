"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { formatDateTime } from "@/components/account/format";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SelectField, toOptions } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { verificationApi, type VerificationApi } from "@/lib/verification/api";
import { COUNTRY_OPTIONS, countryName } from "@/lib/verification/countries";
import { canWithdraw, ID_DOCUMENT_TYPE_LABELS, identityComplete, isEditable, normaliseRequirements, requirementLabel } from "@/lib/verification/labels";
import { FINAL_CASE_STATUSES, type Address, type IdDocumentType, type KycCase, type KycCasePatch } from "@/lib/verification/types";

import { ProblemSummary } from "./problem-summary";
import { CaseStatusBadge, DecisionNotice, DefinitionList, InformationRequests } from "./status";
import { useVerificationFeedback } from "./use-verification-feedback";

/** YYYY-MM-DD that is a real calendar date. */
export function isIsoDate(value: string): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return false;
  const [y, m, d] = value.split("-").map((part) => parseInt(part, 10)) as [number, number, number];
  const date = new Date(Date.UTC(y, m - 1, d));
  return date.getUTCFullYear() === y && date.getUTCMonth() === m - 1 && date.getUTCDate() === d;
}

/** ISO date of today in Africa/Harare (dates of birth are calendar dates, not instants). */
export function todayInHarare(now: Date = new Date()): string {
  return new Intl.DateTimeFormat("en-CA", { timeZone: "Africa/Harare", year: "numeric", month: "2-digit", day: "2-digit" }).format(now);
}

const TEXT = (data: FormData, key: string) => String(data.get(key) ?? "").trim();

/**
 * Builds the PATCH body from the form and validates formats only (the API decides completeness, age and
 * policy). Empty optional fields are left out so a partial save keeps what is already stored; the ID number
 * is only sent when the user typed one, because the API never returns it unmasked.
 */
export function buildKycPatch(data: FormData, now: Date = new Date()): { body: KycCasePatch; errors: Record<string, string> } {
  const body: KycCasePatch = {};
  const errors: Record<string, string> = {};
  const first = TEXT(data, "legal_first_name");
  const last = TEXT(data, "legal_last_name");
  if (first) body.legal_first_name = first.replace(/\s+/g, " ");
  if (last) body.legal_last_name = last.replace(/\s+/g, " ");
  const dob = TEXT(data, "date_of_birth");
  if (dob) {
    if (!isIsoDate(dob)) errors.date_of_birth = "Enter a real date, for example 1990-04-18.";
    else if (dob > todayInHarare(now)) errors.date_of_birth = "Your date of birth can't be in the future.";
    else body.date_of_birth = dob;
  }
  for (const key of ["nationality", "country_of_residence"] as const) {
    const value = TEXT(data, key);
    if (value) {
      if (!/^[A-Z]{2}$/.test(value)) errors[key] = "Choose a country from the list.";
      else body[key] = value;
    }
  }
  const idType = TEXT(data, "id_document_type");
  if (idType) body.id_document_type = idType as IdDocumentType;
  const idNumber = TEXT(data, "id_document_number").replace(/\s+/g, " ");
  if (idNumber) {
    if (idNumber.length < 4 || idNumber.length > 40) errors.id_document_number = "Enter the number exactly as it appears on the document.";
    else body.id_document_number = idNumber;
  }
  const expiry = TEXT(data, "id_document_expiry");
  if (expiry) {
    if (!isIsoDate(expiry)) errors.id_document_expiry = "Enter a real date, for example 2031-01-31.";
    else body.id_document_expiry = expiry;
  } else if (data.has("id_document_expiry")) {
    body.id_document_expiry = null;
  }
  const address = {
    line1: TEXT(data, "residential_address.line1"),
    line2: TEXT(data, "residential_address.line2"),
    city: TEXT(data, "residential_address.city"),
    province: TEXT(data, "residential_address.province"),
    postal_code: TEXT(data, "residential_address.postal_code"),
    country: TEXT(data, "residential_address.country"),
  };
  if (address.line1 || address.city || address.line2 || address.province || address.postal_code) {
    if (!address.line1) errors["residential_address.line1"] = "Enter the first line of your address.";
    if (!address.city) errors["residential_address.city"] = "Enter your town or city.";
    if (!/^[A-Z]{2}$/.test(address.country)) errors["residential_address.country"] = "Choose a country.";
    const value: Address = {
      line1: address.line1,
      line2: address.line2 || null,
      city: address.city,
      province: address.province || null,
      postal_code: address.postal_code || null,
      country: address.country,
    };
    body.residential_address = value;
  }
  return { body, errors };
}

export interface KycIdentityProps {
  initialCase: KycCase | null;
  level: string;
  emailVerified: boolean;
  phoneVerified: boolean;
  api?: VerificationApi;
}

export function KycIdentity({ initialCase, level, emailVerified, phoneVerified, api = verificationApi }: KycIdentityProps) {
  const router = useRouter();
  const [kycCase, setKycCase] = useState<KycCase | null>(initialCase);
  const [step, setStep] = useState<"details" | "review">(() => (initialCase && identityComplete(initialCase.identity) ? "review" : "details"));
  const [busy, setBusy] = useState<null | "start" | "save" | "submit" | "withdraw">(null);
  const [status, setStatus] = useState<string | null>(null);
  const [confirmWithdraw, setConfirmWithdraw] = useState(false);
  const { formError: detailsFormError, fieldErrors: detailsFieldErrors, errorRef: detailsErrorRef, formRef: detailsFormRef, clear: detailsClear, fail: detailsFail, failWith: detailsFailWith } = useVerificationFeedback();
  const { formError: actionsFormError, problems: actionsProblems, errorRef: actionsErrorRef, clear: actionsClear, failWith: actionsFailWith } = useVerificationFeedback();
  const statusRef = useRef<HTMLDivElement>(null);
  const reviewRef = useRef<HTMLHeadingElement>(null);
  const detailsRef = useRef<HTMLHeadingElement>(null);
  const focusAfter = useRef<null | "status" | "review" | "details">(null);

  useEffect(() => {
    const target = focusAfter.current;
    focusAfter.current = null;
    if (target === "status") statusRef.current?.focus();
    if (target === "review") reviewRef.current?.focus();
    if (target === "details") detailsRef.current?.focus();
  });

  const prerequisites = emailVerified && phoneVerified;
  const caseStatus = kycCase?.status;
  const editable = isEditable(caseStatus);
  const closed = !kycCase || FINAL_CASE_STATUSES.has(caseStatus ?? "");
  const verified = level === "IDENTITY_VERIFIED" || level === "PAYOUT_VERIFIED";

  async function start() {
    actionsClear();
    setStatus(null);
    setBusy("start");
    try {
      const created = await api.startKycCase();
      setKycCase(created);
      setStep("details");
      focusAfter.current = "details";
    } catch (error) {
      const described = actionsFailWith(error, "We couldn't start your verification. Please try again.");
      if (described?.code === "CASE_ALREADY_OPEN") {
        try {
          const current = await api.currentKycCase();
          if (current) setKycCase(current);
        } catch {
          // keep the message
        }
      }
    } finally {
      setBusy(null);
    }
  }

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!kycCase) return;
    const { body, errors } = buildKycPatch(new FormData(event.currentTarget));
    if (Object.keys(errors).length > 0) {
      detailsFail("Please correct the errors below.", errors);
      return;
    }
    detailsClear();
    setStatus(null);
    setBusy("save");
    try {
      const updated = await api.updateKycCase(kycCase.id, body, kycCase.version);
      setKycCase(updated);
      setStep("review");
      setStatus("Your details are saved.");
      focusAfter.current = "review";
    } catch (error) {
      detailsFailWith(error, "We couldn't save your details. Please try again.");
    } finally {
      setBusy(null);
    }
  }

  async function submit() {
    if (!kycCase) return;
    actionsClear();
    setStatus(null);
    setBusy("submit");
    try {
      const updated = await api.submitKycCase(kycCase.id);
      setKycCase(updated);
      setStatus("Thank you. Your details were submitted for review. We will email you when there is a decision or if we need more information.");
      focusAfter.current = "status";
      router.refresh();
    } catch (error) {
      actionsFailWith(error, "We couldn't submit your verification. Please try again.");
    } finally {
      setBusy(null);
    }
  }

  async function withdraw() {
    if (!kycCase) return;
    setConfirmWithdraw(false);
    actionsClear();
    setBusy("withdraw");
    try {
      const updated = await api.withdrawKycCase(kycCase.id);
      setKycCase(updated);
      setStatus("Your verification was withdrawn. You can start again at any time.");
      focusAfter.current = "status";
      router.refresh();
    } catch (error) {
      actionsFailWith(error, "We couldn't withdraw your verification. Please try again.");
    } finally {
      setBusy(null);
    }
  }

  const identity = kycCase?.identity ?? null;

  return (
    <div className="space-y-6">
      {status ? <FormStatus ref={statusRef} title={status} /> : null}
      <FormError ref={actionsErrorRef} message={actionsFormError} />
      {actionsProblems.length > 0 ? (
        <Alert tone="danger" title="What needs attention">
          <ProblemSummary problems={actionsProblems} />
        </Alert>
      ) : null}

      {kycCase ? (
        <p className="flex flex-wrap items-center gap-2 text-ink-700">
          <span className="font-semibold">Status:</span> <CaseStatusBadge status={kycCase.status} />
          {kycCase.submitted_at ? <span className="text-sm text-ink-600">Submitted {formatDateTime(kycCase.submitted_at)}</span> : null}
        </p>
      ) : null}

      {kycCase ? <DecisionNotice decision={kycCase.decision} status={kycCase.status} /> : null}
      {kycCase ? <InformationRequests id="requests" requests={kycCase.information_requests} /> : null}

      {closed ? (
        <Card as="section" aria-labelledby="start-title">
          <h2 id="start-title" className="font-display text-xl font-semibold text-ink-900">
            {verified ? "Your identity is verified" : kycCase ? "Start a new verification" : "Verify your identity"}
          </h2>
          {verified ? (
            <p className="mt-2 text-ink-700">There is nothing you need to do.</p>
          ) : !prerequisites ? (
            <div className="mt-3">
              <Alert tone="notice" title="First, confirm your contact details">
                <p>
                  Before you can verify your identity, confirm your {!emailVerified ? "email address" : ""}
                  {!emailVerified && !phoneVerified ? " and " : ""}
                  {!phoneVerified ? "phone number" : ""}.
                </p>
                <p className="mt-2">
                  <Link href="/settings/security" className="font-semibold text-brand-700 underline">
                    Go to security settings
                  </Link>
                </p>
              </Alert>
            </div>
          ) : (
            <>
              <p className="mt-2 text-ink-700">
                You will need your national ID or passport and about ten minutes. Your details are encrypted and only seen by our verification team.
              </p>
              <Button className="mt-4" onClick={start} disabled={busy === "start"}>
                {busy === "start" ? "Starting…" : "Start verification"}
              </Button>
            </>
          )}
        </Card>
      ) : null}

      {kycCase && editable && step === "details" ? (
        <Card as="section" aria-labelledby="details-title">
          <h2 id="details-title" ref={detailsRef} tabIndex={-1} className="font-display text-xl font-semibold text-ink-900 focus:outline-none">
            Step 1: Your personal details
          </h2>
          <p className="mt-1 text-ink-700">Enter your details exactly as they appear on your ID document. All fields are required unless marked optional.</p>
          <form ref={detailsFormRef} noValidate onSubmit={save} className="mt-5 space-y-5">
            <FormError ref={detailsErrorRef} message={detailsFormError} />
            <div className="grid gap-5 sm:grid-cols-2">
              <TextField id="field-legal_first_name" label="First name(s)" name="legal_first_name" autoComplete="given-name" defaultValue={identity?.legal_first_name ?? ""} error={detailsFieldErrors.legal_first_name} maxLength={100} />
              <TextField id="field-legal_last_name" label="Surname" name="legal_last_name" autoComplete="family-name" defaultValue={identity?.legal_last_name ?? ""} error={detailsFieldErrors.legal_last_name} maxLength={100} />
            </div>
            <TextField id="field-date_of_birth" label="Date of birth" name="date_of_birth" type="date" autoComplete="bday" hint="You must be 18 or older." defaultValue={identity?.date_of_birth ?? ""} error={detailsFieldErrors.date_of_birth} />
            <div className="grid gap-5 sm:grid-cols-2">
              <SelectField id="field-nationality" label="Nationality" name="nationality" placeholderOption="Choose a country" options={COUNTRY_OPTIONS} defaultValue={identity?.nationality ?? ""} error={detailsFieldErrors.nationality} />
              <SelectField id="field-country_of_residence" label="Country you live in" name="country_of_residence" placeholderOption="Choose a country" options={COUNTRY_OPTIONS} defaultValue={identity?.country_of_residence ?? ""} error={detailsFieldErrors.country_of_residence} />
            </div>
            <fieldset className="space-y-5">
              <legend className="font-display text-lg font-semibold text-ink-900">Your ID document</legend>
              <SelectField id="field-id_document_type" label="Document type" name="id_document_type" placeholderOption="Choose a document" options={toOptions(ID_DOCUMENT_TYPE_LABELS)} defaultValue={identity?.id_document_type ?? ""} error={detailsFieldErrors.id_document_type} />
              <TextField
                id="field-id_document_number"
                label="Document number"
                name="id_document_number"
                autoComplete="off"
                spellCheck={false}
                autoCapitalize="characters"
                hint={identity?.id_document_number_masked ? `Saved as ${identity.id_document_number_masked}. Leave empty to keep it, or type the full number again to change it.` : "For a Zimbabwe national ID, include the letter and the last two digits, e.g. 63-123456 A 12."}
                error={detailsFieldErrors.id_document_number}
                maxLength={40}
              />
              <TextField id="field-id_document_expiry" label="Expiry date (optional)" name="id_document_expiry" type="date" hint="Required for passports. Leave empty if your ID has no expiry date." defaultValue={identity?.id_document_expiry ?? ""} error={detailsFieldErrors.id_document_expiry} />
            </fieldset>
            <fieldset className="space-y-5">
              <legend className="font-display text-lg font-semibold text-ink-900">Home address</legend>
              <TextField id="field-residential_address" label="Address line 1" name="residential_address.line1" autoComplete="address-line1" defaultValue={identity?.residential_address?.line1 ?? ""} error={detailsFieldErrors["residential_address.line1"] ?? detailsFieldErrors.residential_address} maxLength={200} />
              <TextField label="Address line 2 (optional)" name="residential_address.line2" autoComplete="address-line2" defaultValue={identity?.residential_address?.line2 ?? ""} maxLength={200} />
              <div className="grid gap-5 sm:grid-cols-2">
                <TextField label="Town or city" name="residential_address.city" autoComplete="address-level2" defaultValue={identity?.residential_address?.city ?? ""} error={detailsFieldErrors["residential_address.city"]} maxLength={100} />
                <TextField label="Province (optional)" name="residential_address.province" autoComplete="address-level1" defaultValue={identity?.residential_address?.province ?? ""} maxLength={100} />
              </div>
              <div className="grid gap-5 sm:grid-cols-2">
                <TextField label="Postal code (optional)" name="residential_address.postal_code" autoComplete="postal-code" defaultValue={identity?.residential_address?.postal_code ?? ""} maxLength={20} />
                <SelectField label="Country" name="residential_address.country" options={COUNTRY_OPTIONS} defaultValue={identity?.residential_address?.country ?? "ZW"} error={detailsFieldErrors["residential_address.country"]} />
              </div>
            </fieldset>
            <div className="flex flex-wrap gap-3">
              <SubmitButton busy={busy === "save"} busyLabel="Saving…">
                Save and continue
              </SubmitButton>
              {identityComplete(identity) ? (
                <Button variant="outline" onClick={() => { setStep("review"); focusAfter.current = "review"; }}>
                  Back to review
                </Button>
              ) : null}
            </div>
          </form>
        </Card>
      ) : null}

      {kycCase && !closed && (step === "review" || !editable) ? (
        <Card as="section" aria-labelledby="review-title" id="review">
          <h2 id="review-title" ref={reviewRef} tabIndex={-1} className="font-display text-xl font-semibold text-ink-900 focus:outline-none">
            {editable ? "Step 2: Check and submit" : "Your submitted details"}
          </h2>
          <div className="mt-4">
            <DefinitionList
              items={[
                { term: "Name", value: [identity?.legal_first_name, identity?.legal_last_name].filter(Boolean).join(" ") || "—" },
                { term: "Date of birth", value: identity?.date_of_birth ?? "—" },
                { term: "Nationality", value: countryName(identity?.nationality) },
                { term: "Country you live in", value: countryName(identity?.country_of_residence) },
                { term: "ID document", value: identity?.id_document_type ? ID_DOCUMENT_TYPE_LABELS[identity.id_document_type] ?? identity.id_document_type : "—" },
                { term: "Document number", value: identity?.id_document_number_masked ?? "—" },
                { term: "Expiry date", value: identity?.id_document_expiry ?? "—" },
                {
                  term: "Home address",
                  value: identity?.residential_address
                    ? [identity.residential_address.line1, identity.residential_address.line2, identity.residential_address.city, identity.residential_address.province, identity.residential_address.postal_code, countryName(identity.residential_address.country)].filter(Boolean).join(", ")
                    : "—",
                },
              ]}
            />
          </div>
          <div id="field-documents" tabIndex={-1} className="mt-5 focus:outline-none">
            <h3 className="font-semibold text-ink-900">Documents</h3>
            {kycCase.requirements.length === 0 ? (
              <p className="mt-1 text-ink-700">{kycCase.documents.filter((d) => d.status !== "DELETED").length} uploaded.</p>
            ) : (
              <ul className="mt-2 space-y-1">
                {normaliseRequirements(kycCase.requirements).map((req) => (
                  <li key={(req.document_types ?? [req.document_type]).join("|")} className="flex flex-wrap items-center gap-2 text-ink-700">
                    {requirementLabel(req)}
                    {req.satisfied ? <Badge tone="brand">Provided</Badge> : <Badge tone="gold">Needed</Badge>}
                  </li>
                ))}
              </ul>
            )}
            {editable ? (
              <p className="mt-2">
                <Link href="/dashboard/verification/documents" className="font-semibold text-brand-700 underline">
                  Upload or manage documents
                </Link>
              </p>
            ) : null}
          </div>
          {editable ? (
            <div className="mt-6 flex flex-wrap gap-3">
              <Button onClick={submit} disabled={busy === "submit"}>
                {busy === "submit" ? "Submitting…" : kycCase.status === "ADDITIONAL_INFORMATION_REQUIRED" ? "Submit again" : "Submit for verification"}
              </Button>
              <Button variant="outline" onClick={() => { setStep("details"); focusAfter.current = "details"; }}>
                Edit details
              </Button>
            </div>
          ) : (
            <p className="mt-4 text-ink-700">Your details can&apos;t be changed while they are being reviewed.</p>
          )}
        </Card>
      ) : null}

      {kycCase && canWithdraw(kycCase.status) ? (
        <div>
          <Button variant="ghost" onClick={() => setConfirmWithdraw(true)} disabled={busy === "withdraw"}>
            Withdraw this verification
          </Button>
        </div>
      ) : null}

      {confirmWithdraw ? (
        <Dialog title="Withdraw your verification?" description="Your verification will be closed. Uploaded documents stay attached to the closed case. You can start a new verification later." onClose={() => setConfirmWithdraw(false)}>
          <div className="flex flex-wrap gap-3">
            <Button onClick={withdraw}>Withdraw</Button>
            <Button variant="outline" onClick={() => setConfirmWithdraw(false)}>
              Keep it
            </Button>
          </div>
        </Dialog>
      ) : null}
    </div>
  );
}
