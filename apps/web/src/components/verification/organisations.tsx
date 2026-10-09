"use client";

import Link from "next/link";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { formatDateTime } from "@/components/account/format";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { CheckboxGroup, SelectField, toOptions } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { verificationApi, type VerificationApi } from "@/lib/verification/api";
import { formatOwnership, PERCENT_ERROR_MESSAGES, percentToBasisPoints } from "@/lib/verification/basis-points";
import { COUNTRY_OPTIONS, countryName } from "@/lib/verification/countries";
import { canWithdraw, humanise, ID_DOCUMENT_TYPE_LABELS, isEditable, levelLabel, PERSON_ROLE_LABELS } from "@/lib/verification/labels";
import {
  FINAL_CASE_STATUSES,
  KYB_PERSON_ROLES,
  type IdDocumentType,
  type KybCase,
  type KybPersonInput,
  type KybPersonRole,
  type KybStatus,
  type MyOrganisation,
} from "@/lib/verification/types";

import { isIsoDate } from "./kyc-identity";
import { DocumentManager } from "./document-manager";
import { ProblemSummary } from "./problem-summary";
import { CaseStatusBadge, DecisionNotice, DefinitionList, InformationRequests } from "./status";
import { useVerificationFeedback } from "./use-verification-feedback";

export const ORG_TYPE_LABELS: Record<string, string> = {
  COMPANY: "Company",
  TRUST: "Trust",
  PVO: "Private voluntary organisation (PVO)",
  FAITH_BASED: "Faith-based organisation",
  SCHOOL: "School",
  HEALTH_INSTITUTION: "Health institution",
  COMMUNITY_BASED: "Community-based organisation",
  SPORTS_CLUB: "Sports club",
  OTHER: "Other",
};

// ---------------------------------------------------------------------------------------------------------
// /dashboard/organisations
// ---------------------------------------------------------------------------------------------------------

export function OrganisationsList({ initial, api = verificationApi }: { initial: MyOrganisation[]; api?: VerificationApi }) {
  const [items, setItems] = useState(initial);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const { formError, fieldErrors, errorRef, formRef, clear, fail, failWith } = useVerificationFeedback();
  const noticeRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);

  async function onCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    const name = String(data.get("display_name") ?? "").trim().replace(/\s+/g, " ");
    const type = String(data.get("org_type") ?? "");
    const errors: Record<string, string> = {};
    if (name.length < 2 || name.length > 150) errors.display_name = "Enter a name of 2 to 150 characters.";
    if (!type) errors.org_type = "Choose the type of organisation.";
    if (Object.keys(errors).length > 0) return fail("Please correct the errors below.", errors);
    clear();
    setBusy(true);
    try {
      const org = await api.createOrganisation(name, type);
      setItems((current) => [...current, org]);
      form.reset();
      setNotice(`${org.display_name} was created. You are its administrator.`);
    } catch (error) {
      failWith(error, "We couldn't create the organisation. Please try again.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-6">
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      <section aria-labelledby="org-list-title" className="space-y-4">
        <h2 id="org-list-title" className="font-display text-2xl font-semibold text-ink-900">
          Your organisations
        </h2>
        {items.length === 0 ? (
          <EmptyState title="You are not part of any organisation yet" description="Create one below, or ask an organisation's administrator to invite you." />
        ) : (
          <ul className="space-y-3">
            {items.map((org) => (
              <Card as="li" key={org.id} className="flex flex-wrap items-center justify-between gap-3">
                <div className="min-w-0">
                  <p className="font-display text-lg font-semibold break-words text-ink-900">{org.display_name}</p>
                  <p className="flex flex-wrap items-center gap-2 text-sm text-ink-600">
                    <span>{ORG_TYPE_LABELS[org.org_type] ?? humanise(org.org_type)}</span>
                    <Badge tone={org.my_role === "ORG_ADMIN" ? "brand" : "neutral"}>{org.my_role === "ORG_ADMIN" ? "Administrator" : "Member"}</Badge>
                    {org.status !== "ACTIVE" ? <Badge tone="gold">{humanise(org.status)}</Badge> : null}
                  </p>
                </div>
                <Link href={`/dashboard/organisations/${encodeURIComponent(org.id)}/verification`} className="inline-flex min-h-11 items-center font-semibold text-brand-700 underline">
                  Verification<span className="sr-only"> for {org.display_name}</span>
                </Link>
              </Card>
            ))}
          </ul>
        )}
      </section>
      <Card as="section" aria-labelledby="new-org-title">
        <h2 id="new-org-title" className="font-display text-xl font-semibold text-ink-900">
          Create an organisation
        </h2>
        <form ref={formRef} noValidate onSubmit={onCreate} className="mt-4 space-y-5">
          <FormError ref={errorRef} message={formError} />
          <TextField label="Organisation name" name="display_name" autoComplete="organization" error={fieldErrors.display_name} maxLength={150} />
          <SelectField label="Type of organisation" name="org_type" placeholderOption="Choose a type" options={toOptions(ORG_TYPE_LABELS)} error={fieldErrors.org_type} />
          <SubmitButton busy={busy} busyLabel="Creating…">
            Create organisation
          </SubmitButton>
        </form>
      </Card>
    </div>
  );
}

// ---------------------------------------------------------------------------------------------------------
// /dashboard/organisations/[id]/verification
// ---------------------------------------------------------------------------------------------------------

export function buildPersonInput(data: FormData): { body: KybPersonInput; errors: Record<string, string> } {
  const errors: Record<string, string> = {};
  const text = (key: string) => String(data.get(key) ?? "").trim();
  const fullName = text("full_name").replace(/\s+/g, " ");
  const roles = data.getAll("roles").map(String).filter((r): r is KybPersonRole => (KYB_PERSON_ROLES as readonly string[]).includes(r));
  if (fullName.length < 2) errors.full_name = "Enter the person's full legal name.";
  if (roles.length === 0) errors.roles = "Choose at least one role.";
  const body: KybPersonInput = { full_name: fullName, roles };
  const ownership = text("ownership_percent");
  if (ownership) {
    const parsed = percentToBasisPoints(ownership);
    if (!parsed.ok) errors.ownership_percent = PERCENT_ERROR_MESSAGES[parsed.error];
    else body.ownership_bp = parsed.basisPoints;
  } else if (roles.includes("BENEFICIAL_OWNER")) {
    errors.ownership_percent = "Enter the ownership share of a beneficial owner.";
  }
  const dob = text("date_of_birth");
  if (dob) {
    if (!isIsoDate(dob)) errors.date_of_birth = "Enter a real date, for example 1975-02-28.";
    else body.date_of_birth = dob;
  }
  const nationality = text("nationality");
  if (nationality) body.nationality = nationality;
  const idType = text("id_document_type");
  if (idType) body.id_document_type = idType as IdDocumentType;
  const idNumber = text("id_document_number").replace(/\s+/g, " ");
  if (idNumber) body.id_document_number = idNumber;
  if (idNumber && !idType) errors.id_document_type = "Choose the type of ID document.";
  return { body, errors };
}

/** Sum of ownership shares in basis points (integers only). */
export function totalOwnershipBp(persons: Array<{ ownership_bp: number | null }>): number {
  return persons.reduce((sum, p) => sum + (p.ownership_bp ?? 0), 0);
}

interface HistoryItem {
  at: string;
  text: string;
}

export function kybHistory(kyb: KybCase): HistoryItem[] {
  const items: HistoryItem[] = [{ at: kyb.created_at, text: "Verification started" }];
  if (kyb.submitted_at) items.push({ at: kyb.submitted_at, text: "Submitted for review" });
  for (const req of kyb.information_requests) {
    items.push({ at: req.requested_at, text: "Reviewer asked for more information" });
    if (req.responded_at) items.push({ at: req.responded_at, text: "Information provided and resubmitted" });
  }
  if (kyb.decision) items.push({ at: kyb.decision.decided_at, text: kyb.decision.outcome === "APPROVED" ? "Verified" : `Decision: ${humanise(kyb.decision.outcome)}` });
  if (kyb.status === "WITHDRAWN") items.push({ at: kyb.updated_at, text: "Withdrawn" });
  return items.sort((a, b) => (a.at < b.at ? -1 : a.at > b.at ? 1 : 0));
}

export interface OrganisationVerificationProps {
  organisation: MyOrganisation;
  initial: KybStatus;
  representativeVerified: boolean;
  api?: VerificationApi;
}

export function OrganisationVerification({ organisation, initial, representativeVerified, api = verificationApi }: OrganisationVerificationProps) {
  const isAdmin = organisation.my_role === "ORG_ADMIN";
  const [kyb, setKyb] = useState<KybCase | null>(initial.case);
  const [busy, setBusy] = useState<null | "start" | "details" | "person" | "submit" | "withdraw" | string>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<null | "withdraw" | { personId: string; name: string }>(null);
  const { formError: actionsFormError, problems: actionsProblems, errorRef: actionsErrorRef, clear: actionsClear, failWith: actionsFailWith } = useVerificationFeedback();
  const { formError: detailsFormError, fieldErrors: detailsFieldErrors, errorRef: detailsErrorRef, formRef: detailsFormRef, fail: detailsFail, clear: detailsClear, failWith: detailsFailWith } = useVerificationFeedback();
  const { formError: personFormError, fieldErrors: personFieldErrors, errorRef: personErrorRef, formRef: personFormRef, fail: personFail, clear: personClear, failWith: personFailWith } = useVerificationFeedback();
  const sinks = {
    actions: { clear: actionsClear, failWith: actionsFailWith },
    details: { clear: detailsClear, failWith: detailsFailWith },
    person: { clear: personClear, failWith: personFailWith },
  };
  const statusRef = useRef<HTMLDivElement>(null);
  const orgId = organisation.id;
  const closed = !kyb || FINAL_CASE_STATUSES.has(kyb.status);
  const editable = isAdmin && !!kyb && isEditable(kyb.status);

  useEffect(() => {
    if (status) statusRef.current?.focus();
  }, [status]);

  async function run<T>(key: string, fn: () => Promise<T>, onOk: (value: T) => void, sink: keyof typeof sinks = "actions", fallback?: string) {
    const feedback = sinks[sink];
    feedback.clear();
    setStatus(null);
    setBusy(key);
    try {
      onOk(await fn());
    } catch (error) {
      feedback.failWith(error, fallback);
    } finally {
      setBusy(null);
    }
  }

  function saveDetails(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const text = (key: string) => String(data.get(key) ?? "").trim();
    const body: Record<string, unknown> = {};
    for (const key of ["registered_name", "trading_name", "registration_number", "registry", "country_of_registration"]) {
      const value = text(key);
      if (value) body[key] = value;
    }
    const line1 = text("registered_address.line1");
    const city = text("registered_address.city");
    if (line1 || city) {
      const errors: Record<string, string> = {};
      if (!line1) errors["registered_address.line1"] = "Enter the first line of the registered address.";
      if (!city) errors["registered_address.city"] = "Enter the town or city.";
      if (Object.keys(errors).length > 0) return detailsFail("Please correct the errors below.", errors);
      body.registered_address = {
        line1,
        line2: text("registered_address.line2") || null,
        city,
        province: text("registered_address.province") || null,
        postal_code: text("registered_address.postal_code") || null,
        country: text("registered_address.country") || "ZW",
      };
    }
    void run("details", () => api.updateKyb(orgId, body), (updated) => {
      setKyb(updated);
      setStatus("Organisation details saved.");
    }, "details", "We couldn't save the details. Please try again.");
  }

  function addPerson(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const { body, errors } = buildPersonInput(new FormData(form));
    if (Object.keys(errors).length > 0) return personFail("Please correct the errors below.", errors);
    void run("person", () => api.addKybPerson(orgId, body), (created) => {
      setKyb((current) => (current ? { ...current, persons: [...current.persons, created] } : current));
      form.reset();
      setStatus(`${created.full_name} was added.`);
    }, "person", "We couldn't add this person. Please try again.");
  }

  function removePerson(personId: string, name: string) {
    setConfirm(null);
    void run(`remove-${personId}`, () => api.removeKybPerson(orgId, personId), () => {
      setKyb((current) => (current ? { ...current, persons: current.persons.filter((p) => p.id !== personId) } : current));
      setStatus(`${name} was removed.`);
    });
  }

  const total = kyb ? totalOwnershipBp(kyb.persons) : 0;

  return (
    <div className="space-y-6">
      {status ? <FormStatus ref={statusRef} title={status} /> : null}
      <FormError ref={actionsErrorRef} message={actionsFormError} />
      {actionsProblems.length > 0 ? (
        <Alert tone="danger" title="What needs attention">
          <ProblemSummary problems={actionsProblems} />
        </Alert>
      ) : null}

      <Card as="section" aria-labelledby="org-status-title">
        <h2 id="org-status-title" className="font-display text-xl font-semibold text-ink-900">
          Status
        </h2>
        <div className="mt-4">
          <DefinitionList
            items={[
              { term: "Organisation", value: organisation.display_name },
              { term: "Type", value: ORG_TYPE_LABELS[organisation.org_type] ?? humanise(organisation.org_type) },
              { term: "Verification level", value: levelLabel(initial.level) },
              { term: "Current verification", value: <CaseStatusBadge status={kyb?.status} /> },
            ]}
          />
        </div>
        {!isAdmin ? <p className="mt-4 text-ink-700">Only the organisation&apos;s administrators can manage its verification. You can see its status here.</p> : null}
      </Card>

      {kyb ? <DecisionNotice decision={kyb.decision} status={kyb.status} /> : null}
      {isAdmin && kyb ? <InformationRequests requests={kyb.information_requests} heading="Requests from our reviewers" /> : null}

      {isAdmin ? (
        <Card as="section" aria-labelledby="rep-title">
          <h2 id="rep-title" className="font-display text-xl font-semibold text-ink-900">
            You, as the organisation&apos;s representative
          </h2>
          <p className="mt-2 flex flex-wrap items-center gap-2 text-ink-700">
            The person who submits this verification must have verified their own identity.
            {representativeVerified ? <Badge tone="brand">Your identity is verified</Badge> : <Badge tone="gold">Your identity is not verified yet</Badge>}
          </p>
          {!representativeVerified ? (
            <p className="mt-2">
              <Link href="/dashboard/verification/identity" className="font-semibold text-brand-700 underline">
                Verify your identity
              </Link>
            </p>
          ) : null}
        </Card>
      ) : null}

      {isAdmin && closed ? (
        <Card as="section" aria-labelledby="kyb-start-title">
          <h2 id="kyb-start-title" className="font-display text-xl font-semibold text-ink-900">
            {kyb ? "Start a new organisation verification" : "Verify this organisation"}
          </h2>
          <p className="mt-2 text-ink-700">You will need the registration documents and details of the people who run, own or control the organisation.</p>
          <Button className="mt-4" disabled={busy === "start"} onClick={() => run("start", () => api.startKyb(orgId), (created) => setKyb(created), "actions", "We couldn't start the verification. Please try again.")}>
            {busy === "start" ? "Starting…" : "Start verification"}
          </Button>
        </Card>
      ) : null}

      {isAdmin && kyb && !closed ? (
        <>
          <Card as="section" aria-labelledby="kyb-details-title">
            <h2 id="kyb-details-title" className="font-display text-xl font-semibold text-ink-900">
              Organisation details
            </h2>
            {editable ? (
              <form ref={detailsFormRef} noValidate onSubmit={saveDetails} className="mt-4 space-y-5">
                <FormError ref={detailsErrorRef} message={detailsFormError} />
                <TextField id="field-registered_name" label="Registered name" name="registered_name" defaultValue={kyb.details.registered_name ?? ""} error={detailsFieldErrors.registered_name} maxLength={200} />
                <TextField label="Trading name (optional)" name="trading_name" defaultValue={kyb.details.trading_name ?? ""} maxLength={200} />
                <div className="grid gap-5 sm:grid-cols-2">
                  <TextField id="field-registration_number" label="Registration number" name="registration_number" defaultValue={kyb.details.registration_number ?? ""} error={detailsFieldErrors.registration_number} maxLength={60} autoComplete="off" />
                  <TextField id="field-registry" label="Registry" name="registry" hint="Where it is registered, e.g. Companies Registry, PVO Registry, Deeds Office." defaultValue={kyb.details.registry ?? ""} error={detailsFieldErrors.registry} maxLength={120} />
                </div>
                <SelectField id="field-country_of_registration" label="Country of registration" name="country_of_registration" options={COUNTRY_OPTIONS} defaultValue={kyb.details.country_of_registration ?? "ZW"} error={detailsFieldErrors.country_of_registration} />
                <fieldset id="field-registered_address" tabIndex={-1} className="space-y-5">
                  <legend className="font-display text-lg font-semibold text-ink-900">Registered address</legend>
                  <TextField label="Address line 1" name="registered_address.line1" autoComplete="off" defaultValue={kyb.details.registered_address?.line1 ?? ""} error={detailsFieldErrors["registered_address.line1"]} maxLength={200} />
                  <TextField label="Address line 2 (optional)" name="registered_address.line2" autoComplete="off" defaultValue={kyb.details.registered_address?.line2 ?? ""} maxLength={200} />
                  <div className="grid gap-5 sm:grid-cols-2">
                    <TextField label="Town or city" name="registered_address.city" autoComplete="off" defaultValue={kyb.details.registered_address?.city ?? ""} error={detailsFieldErrors["registered_address.city"]} maxLength={100} />
                    <TextField label="Province (optional)" name="registered_address.province" autoComplete="off" defaultValue={kyb.details.registered_address?.province ?? ""} maxLength={100} />
                  </div>
                  <div className="grid gap-5 sm:grid-cols-2">
                    <TextField label="Postal code (optional)" name="registered_address.postal_code" autoComplete="off" defaultValue={kyb.details.registered_address?.postal_code ?? ""} maxLength={20} />
                    <SelectField label="Country" name="registered_address.country" options={COUNTRY_OPTIONS} defaultValue={kyb.details.registered_address?.country ?? "ZW"} />
                  </div>
                </fieldset>
                <SubmitButton busy={busy === "details"} busyLabel="Saving…">
                  Save details
                </SubmitButton>
              </form>
            ) : (
              <div className="mt-4">
                <DefinitionList
                  items={[
                    { term: "Registered name", value: kyb.details.registered_name ?? "—" },
                    { term: "Trading name", value: kyb.details.trading_name ?? "—" },
                    { term: "Registration number", value: kyb.details.registration_number ?? "—" },
                    { term: "Registry", value: kyb.details.registry ?? "—" },
                    { term: "Country of registration", value: countryName(kyb.details.country_of_registration) },
                  ]}
                />
              </div>
            )}
          </Card>

          <Card as="section" aria-labelledby="persons-title" id="field-persons" tabIndex={-1} className="focus:outline-none">
            <h2 id="persons-title" className="font-display text-xl font-semibold text-ink-900">
              Directors, trustees and owners
            </h2>
            <p className="mt-2 text-ink-700">List everyone who runs, owns or controls the organisation. Ownership is entered as a percentage.</p>
            {kyb.persons.length === 0 ? (
              <p className="mt-4 text-ink-700">Nobody added yet.</p>
            ) : (
              <div className="mt-4 overflow-x-auto">
                <table className="w-full min-w-[32rem] text-left text-ink-700">
                  <caption className="sr-only">People connected to the organisation</caption>
                  <thead>
                    <tr className="border-b border-line text-sm text-ink-600">
                      <th scope="col" className="py-2 pr-3">Name</th>
                      <th scope="col" className="py-2 pr-3">Roles</th>
                      <th scope="col" className="py-2 pr-3">Ownership</th>
                      <th scope="col" className="py-2 pr-3">ID</th>
                      {editable ? <th scope="col" className="py-2"><span className="sr-only">Actions</span></th> : null}
                    </tr>
                  </thead>
                  <tbody>
                    {kyb.persons.map((p) => (
                      <tr key={p.id} className="border-b border-line align-top">
                        <th scope="row" className="py-2 pr-3 font-semibold break-words text-ink-900">{p.full_name}</th>
                        <td className="py-2 pr-3">{p.roles.map((r) => PERSON_ROLE_LABELS[r] ?? r).join(", ")}</td>
                        <td className="py-2 pr-3">{formatOwnership(p.ownership_bp)}</td>
                        <td className="py-2 pr-3">{p.id_document_number_masked ? `${ID_DOCUMENT_TYPE_LABELS[p.id_document_type ?? ""] ?? ""} ${p.id_document_number_masked}` : "—"}</td>
                        {editable ? (
                          <td className="py-2">
                            <Button variant="ghost" disabled={busy === `remove-${p.id}`} onClick={() => setConfirm({ personId: p.id, name: p.full_name })} aria-label={`Remove ${p.full_name}`}>
                              Remove
                            </Button>
                          </td>
                        ) : null}
                      </tr>
                    ))}
                  </tbody>
                  <tfoot>
                    <tr>
                      <th scope="row" colSpan={2} className="py-2 pr-3 text-sm text-ink-600">
                        Total recorded ownership
                      </th>
                      <td className="py-2 pr-3 font-semibold">{formatOwnership(Math.min(total, 10_000))}{total > 10_000 ? " (more than 100% — check the shares)" : ""}</td>
                    </tr>
                  </tfoot>
                </table>
              </div>
            )}
            {editable ? (
              <form ref={personFormRef} noValidate onSubmit={addPerson} className="mt-6 space-y-5 rounded-xl border border-line bg-surface-muted p-4" aria-labelledby="add-person-title">
                <h3 id="add-person-title" className="font-display text-lg font-semibold text-ink-900">
                  Add a person
                </h3>
                <FormError ref={personErrorRef} message={personFormError} />
                <TextField label="Full legal name" name="full_name" autoComplete="off" error={personFieldErrors.full_name} maxLength={200} />
                <CheckboxGroup legend="Roles" name="roles" options={toOptions(PERSON_ROLE_LABELS)} error={personFieldErrors.roles} />
                <TextField label="Ownership share (%) — optional unless a beneficial owner" name="ownership_percent" inputMode="decimal" hint="For example 25 or 12.5 (up to two decimal places)." error={personFieldErrors.ownership_percent ?? personFieldErrors.ownership_bp} maxLength={7} autoComplete="off" />
                <div className="grid gap-5 sm:grid-cols-2">
                  <TextField label="Date of birth (optional)" name="date_of_birth" type="date" error={personFieldErrors.date_of_birth} />
                  <SelectField label="Nationality (optional)" name="nationality" placeholderOption="Choose a country" options={COUNTRY_OPTIONS} />
                </div>
                <div className="grid gap-5 sm:grid-cols-2">
                  <SelectField label="ID document type (optional)" name="id_document_type" placeholderOption="Choose a document" options={toOptions(ID_DOCUMENT_TYPE_LABELS)} error={personFieldErrors.id_document_type} />
                  <TextField label="ID document number (optional)" name="id_document_number" autoComplete="off" spellCheck={false} error={personFieldErrors.id_document_number} maxLength={40} />
                </div>
                <SubmitButton busy={busy === "person"} busyLabel="Adding…">
                  Add person
                </SubmitButton>
              </form>
            ) : null}
            {kyb.persons.length > 0 ? (
              <div className="mt-6 space-y-3">
                <h3 className="font-display text-lg font-semibold text-ink-900">ID documents of these people</h3>
                {kyb.persons.map((p) => (
                  <details key={p.id} className="rounded-xl border border-line p-3">
                    <summary className="min-h-11 cursor-pointer py-2 font-semibold text-ink-900">ID for {p.full_name}</summary>
                    <div className="mt-3">
                      <DocumentManager subjectType="KYB_PERSON" subjectId={p.id} initialDocuments={kyb.documents.filter((d) => d.subject_type === "KYB_PERSON" && d.subject_id === p.id)} editable={editable} heading={`ID documents: ${p.full_name}`} level={3} anchor={`person-${p.id}`} api={api} />
                    </div>
                  </details>
                ))}
              </div>
            ) : null}
          </Card>

          <Card>
            <DocumentManager
              subjectType="KYB_CASE"
              subjectId={kyb.id}
              initialDocuments={kyb.documents.filter((d) => d.subject_type !== "KYB_PERSON")}
              requirements={kyb.requirements}
              editable={editable}
              heading="Registration documents"
              api={api}
            />
          </Card>

          <Card as="section" aria-labelledby="kyb-submit-title">
            <h2 id="kyb-submit-title" className="font-display text-xl font-semibold text-ink-900">
              Submit
            </h2>
            {editable ? (
              <>
                <p className="mt-2 text-ink-700">When the details, people and documents are complete, submit the organisation for review.</p>
                <div className="mt-4 flex flex-wrap gap-3">
                  <Button disabled={busy === "submit"} onClick={() => run("submit", () => api.submitKyb(orgId), (updated) => { setKyb(updated); setStatus("Submitted for review. We will let you know when there is a decision."); }, "actions", "We couldn't submit. Please try again.")}>
                    {busy === "submit" ? "Submitting…" : kyb.status === "ADDITIONAL_INFORMATION_REQUIRED" ? "Submit again" : "Submit for verification"}
                  </Button>
                </div>
              </>
            ) : (
              <p className="mt-2 text-ink-700">This verification is being reviewed and can&apos;t be changed.</p>
            )}
            {canWithdraw(kyb.status) ? (
              <Button variant="ghost" className="mt-3" onClick={() => setConfirm("withdraw")} disabled={busy === "withdraw"}>
                Withdraw this verification
              </Button>
            ) : null}
          </Card>
        </>
      ) : null}

      {isAdmin && kyb ? (
        <Card as="section" aria-labelledby="history-title">
          <h2 id="history-title" className="font-display text-xl font-semibold text-ink-900">
            Submission history
          </h2>
          <ol className="mt-3 space-y-2">
            {kybHistory(kyb).map((item, index) => (
              <li key={`${item.at}-${index}`} className="flex flex-wrap justify-between gap-2 text-ink-700">
                <span>{item.text}</span>
                <span className="text-sm text-ink-600">{formatDateTime(item.at)}</span>
              </li>
            ))}
          </ol>
        </Card>
      ) : null}

      {confirm === "withdraw" ? (
        <Dialog title="Withdraw this verification?" description="The organisation's verification will be closed. You can start a new one later." onClose={() => setConfirm(null)}>
          <div className="flex flex-wrap gap-3">
            <Button onClick={() => { setConfirm(null); void run("withdraw", () => api.withdrawKyb(orgId), (updated) => { setKyb(updated); setStatus("The verification was withdrawn."); }); }}>Withdraw</Button>
            <Button variant="outline" onClick={() => setConfirm(null)}>
              Keep it
            </Button>
          </div>
        </Dialog>
      ) : null}
      {confirm && typeof confirm === "object" ? (
        <Dialog title={`Remove ${confirm.name}?`} description="They will be removed from this verification." onClose={() => setConfirm(null)}>
          <div className="flex flex-wrap gap-3">
            <Button onClick={() => removePerson(confirm.personId, confirm.name)}>Remove</Button>
            <Button variant="outline" onClick={() => setConfirm(null)}>
              Keep
            </Button>
          </div>
        </Dialog>
      ) : null}
    </div>
  );
}
