"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { FormError, FormStatus } from "@/components/forms/form-error";
import { SelectField, TextAreaField, toOptions } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { verificationApi, type VerificationApi } from "@/lib/verification/api";
import {
  AUTHORITY_BASIS_LABELS,
  BENEFICIARY_TYPE_LABELS,
  beneficiaryNeedsExtraEvidence,
  isEditable,
  RELATIONSHIP_LABELS,
} from "@/lib/verification/labels";
import type { AuthorityBasis, Beneficiary, BeneficiaryInput, BeneficiaryType, MyOrganisation, RelationshipType } from "@/lib/verification/types";

import { isIsoDate, todayInHarare } from "./kyc-identity";
import { DocumentManager } from "./document-manager";
import { ProblemSummary } from "./problem-summary";
import { CaseStatusBadge, DecisionNotice, DefinitionList, InformationRequests } from "./status";
import { useVerificationFeedback } from "./use-verification-feedback";

const INDIVIDUAL_TYPES: ReadonlySet<string> = new Set(["SELF", "INDIVIDUAL", "MINOR", "INCAPACITATED_ADULT"]);

/** Suggested authority basis per beneficiary type (a suggestion only; the user chooses and the API decides). */
export const SUGGESTED_AUTHORITY: Record<string, AuthorityBasis> = {
  SELF: "NOT_REQUIRED",
  INDIVIDUAL: "BENEFICIARY_CONSENT",
  MINOR: "PARENTAL_RESPONSIBILITY",
  INCAPACITATED_ADULT: "LEGAL_REPRESENTATION",
  ORGANISATION: "ORGANISATION_AUTHORITY",
  INSTITUTION: "INSTITUTION_CONFIRMATION",
  COMMUNITY_GROUP: "GROUP_MANDATE",
};

export function extraEvidenceText(type: string): string | null {
  if (type === "MINOR") {
    return "Funds for a child need extra evidence: the child's birth certificate and proof that you are their parent or legal guardian (or a guardianship order). A reviewer checks these, and a second reviewer must approve.";
  }
  if (type === "INCAPACITATED_ADULT") {
    return "Funds for an adult who cannot act for themselves need extra evidence: proof of your legal authority (for example a court order or power of attorney) and, where relevant, a letter from a medical institution.";
  }
  return null;
}

export function buildBeneficiaryInput(data: FormData, now: Date = new Date()): { body: BeneficiaryInput; errors: Record<string, string> } {
  const errors: Record<string, string> = {};
  const text = (key: string) => String(data.get(key) ?? "").trim();
  const type = text("beneficiary_type") as BeneficiaryType;
  const relationshipType = text("relationship_type") as RelationshipType;
  const authority = text("authority_basis") as AuthorityBasis;
  const displayName = text("display_name").replace(/\s+/g, " ");
  const fullName = text("full_name").replace(/\s+/g, " ");
  const dob = text("date_of_birth");
  const description = text("relationship_description");
  if (!type) errors.beneficiary_type = "Choose who the funds are for.";
  if (displayName.length < 2) errors.display_name = "Enter a name of at least 2 characters.";
  if (!relationshipType) errors.relationship_type = "Choose your relationship to the beneficiary.";
  if (relationshipType === "OTHER" && !description) errors.relationship_description = "Describe your relationship.";
  if (!authority) errors.authority_basis = "Choose what gives you authority to raise funds for them.";
  if (type && INDIVIDUAL_TYPES.has(type) && type !== "SELF" && !fullName) errors.full_name = "Enter their full legal name.";
  if (dob) {
    if (!isIsoDate(dob)) errors.date_of_birth = "Enter a real date, for example 2015-06-01.";
    else if (dob > todayInHarare(now)) errors.date_of_birth = "The date of birth can't be in the future.";
  } else if (type === "MINOR") {
    errors.date_of_birth = "Enter the child's date of birth.";
  }
  const body: BeneficiaryInput = {
    beneficiary_type: type,
    display_name: displayName,
    relationship: description ? { type: relationshipType, description } : { type: relationshipType },
    authority_basis: authority,
  };
  const owner = text("owner_organisation_id");
  if (owner) body.owner_organisation_id = owner;
  if (fullName) body.full_name = fullName;
  if (dob && !errors.date_of_birth) body.date_of_birth = dob;
  return { body, errors };
}

function BeneficiaryForm({
  initial,
  organisations,
  busy,
  submitLabel,
  formRef,
  errorRef,
  formError,
  fieldErrors,
  onSubmit,
  onCancel,
}: {
  initial?: Beneficiary;
  organisations: MyOrganisation[];
  busy: boolean;
  submitLabel: string;
  formRef: React.RefObject<HTMLFormElement | null>;
  errorRef: React.RefObject<HTMLDivElement | null>;
  formError: string | null;
  fieldErrors: Record<string, string>;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  onCancel?: () => void;
}) {
  const [type, setType] = useState<string>(initial?.beneficiary_type ?? "");
  const [authority, setAuthority] = useState<string>(initial?.authority_basis ?? "");
  const extra = extraEvidenceText(type);
  const adminOrgs = organisations.filter((o) => o.my_role === "ORG_ADMIN");
  const editing = !!initial;
  return (
    <form ref={formRef} noValidate onSubmit={onSubmit} className="space-y-5">
      <FormError ref={errorRef} message={formError} />
      {!editing && adminOrgs.length > 0 ? (
        <SelectField
          label="Who is raising the funds?"
          name="owner_organisation_id"
          options={[{ value: "", label: "Me, personally" }, ...adminOrgs.map((o) => ({ value: o.id, label: `${o.display_name} (organisation)` }))]}
          defaultValue=""
        />
      ) : null}
      {!editing ? (
        <SelectField
          label="Who are the funds for?"
          name="beneficiary_type"
          placeholderOption="Choose one"
          options={toOptions(BENEFICIARY_TYPE_LABELS)}
          value={type}
          onChange={(event) => {
            setType(event.target.value);
            if (!authority || Object.values(SUGGESTED_AUTHORITY).includes(authority as AuthorityBasis)) setAuthority(SUGGESTED_AUTHORITY[event.target.value] ?? "");
          }}
          error={fieldErrors.beneficiary_type}
        />
      ) : (
        <>
          <input type="hidden" name="beneficiary_type" value={type} />
          <p className="text-ink-700">
            <span className="font-semibold">Beneficiary type:</span> {BENEFICIARY_TYPE_LABELS[type] ?? type}
          </p>
        </>
      )}
      {extra ? (
        <Alert tone="notice" title="Extra evidence needed">
          {extra}
        </Alert>
      ) : null}
      <TextField label="Name to show on campaigns" name="display_name" hint="How the beneficiary will be named publicly. For a child, consider using a first name only." defaultValue={initial?.display_name ?? ""} error={fieldErrors.display_name} maxLength={150} />
      {type === "" || INDIVIDUAL_TYPES.has(type) ? (
        <>
          <TextField label={type === "SELF" ? "Full legal name (optional)" : "Full legal name"} name="full_name" hint="Only our verification team sees this." defaultValue={initial?.full_name ?? ""} error={fieldErrors.full_name} maxLength={200} autoComplete="off" />
          <TextField label={type === "MINOR" ? "Date of birth" : "Date of birth (optional)"} name="date_of_birth" type="date" defaultValue={initial?.date_of_birth ?? ""} error={fieldErrors.date_of_birth} />
        </>
      ) : (
        <TextField label="Registered or full name (optional)" name="full_name" defaultValue={initial?.full_name ?? ""} error={fieldErrors.full_name} maxLength={200} />
      )}
      <SelectField label="Your relationship to them" name="relationship_type" placeholderOption="Choose one" options={toOptions(RELATIONSHIP_LABELS)} defaultValue={initial?.relationship.type ?? ""} error={fieldErrors.relationship_type ?? fieldErrors.relationship} />
      <TextAreaField label="Describe the relationship (optional unless you chose Other)" name="relationship_description" rows={2} defaultValue={initial?.relationship.description ?? ""} error={fieldErrors.relationship_description} maxLength={500} />
      <SelectField
        label="What gives you authority to raise funds for them?"
        name="authority_basis"
        hint="Your relationship alone does not give authority. Choose the basis you can prove with documents."
        placeholderOption="Choose one"
        options={toOptions(AUTHORITY_BASIS_LABELS)}
        value={authority}
        onChange={(event) => setAuthority(event.target.value)}
        error={fieldErrors.authority_basis}
      />
      <div className="flex flex-wrap gap-3">
        <SubmitButton busy={busy} busyLabel="Saving…">
          {submitLabel}
        </SubmitButton>
        {onCancel ? (
          <Button variant="outline" onClick={onCancel}>
            Cancel
          </Button>
        ) : null}
      </div>
    </form>
  );
}

function BeneficiaryCard({ initial, organisations, api }: { initial: Beneficiary; organisations: MyOrganisation[]; api: VerificationApi }) {
  const router = useRouter();
  const [item, setItem] = useState(initial);
  const [editing, setEditing] = useState(false);
  const [busy, setBusy] = useState<null | "save" | "submit">(null);
  const [status, setStatus] = useState<string | null>(null);
  const { formError: editFormError, fieldErrors: editFieldErrors, errorRef: editErrorRef, formRef: editFormRef, clear: editClear, fail: editFail, failWith: editFailWith } = useVerificationFeedback();
  const { formError: actionsFormError, problems: actionsProblems, errorRef: actionsErrorRef, clear: actionsClear, failWith: actionsFailWith } = useVerificationFeedback();
  const statusRef = useRef<HTMLDivElement>(null);
  const v = item.verification;
  const editable = isEditable(v.status);
  const titleId = `beneficiary-${item.id}`;

  useEffect(() => {
    if (status) statusRef.current?.focus();
  }, [status]);

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const { body, errors } = buildBeneficiaryInput(new FormData(event.currentTarget));
    if (Object.keys(errors).length > 0) return editFail("Please correct the errors below.", errors);
    editClear();
    setBusy("save");
    try {
      const patch: Partial<BeneficiaryInput> = {
        display_name: body.display_name,
        relationship: body.relationship,
        authority_basis: body.authority_basis,
        ...(body.full_name ? { full_name: body.full_name } : {}),
        ...(body.date_of_birth ? { date_of_birth: body.date_of_birth } : {}),
      };
      setItem(await api.updateBeneficiary(item.id, patch, item.version));
      setEditing(false);
      setStatus("Changes saved.");
    } catch (error) {
      editFailWith(error, "We couldn't save the changes. Please try again.", { "relationship.type": "relationship_type", "relationship.description": "relationship_description" });
    } finally {
      setBusy(null);
    }
  }

  async function submit() {
    actionsClear();
    setStatus(null);
    setBusy("submit");
    try {
      setItem(await api.submitBeneficiary(item.id));
      setStatus("Submitted for verification. We will let you know when it has been reviewed.");
      router.refresh();
    } catch (error) {
      actionsFailWith(error, "We couldn't submit this beneficiary. Please try again.");
    } finally {
      setBusy(null);
    }
  }

  return (
    <Card as="li" aria-labelledby={titleId} className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 id={titleId} className="font-display text-xl font-semibold break-words text-ink-900">
            {item.display_name}
          </h3>
          <p className="mt-1 flex flex-wrap items-center gap-2 text-ink-700">
            <CaseStatusBadge status={v.status} />
            {beneficiaryNeedsExtraEvidence(item.beneficiary_type) ? <Badge tone="gold">Extra evidence needed</Badge> : null}
            {v.requires_second_approval ? <Badge tone="info">Two reviewers required</Badge> : null}
            {item.owner.type === "ORGANISATION" ? <Badge tone="neutral">{organisations.find((o) => o.id === item.owner.id)?.display_name ?? "Organisation"}</Badge> : null}
          </p>
        </div>
        {editable && !editing ? (
          <Button variant="outline" onClick={() => setEditing(true)} aria-label={`Edit details of ${item.display_name}`}>
            Edit details
          </Button>
        ) : null}
      </div>
      {status ? <FormStatus ref={statusRef} title={status} /> : null}
      <FormError ref={actionsErrorRef} message={actionsFormError} />
      {actionsProblems.length > 0 ? (
        <Alert tone="danger" title="What needs attention">
          <ProblemSummary problems={actionsProblems} />
        </Alert>
      ) : null}
      {editing ? (
        <BeneficiaryForm initial={item} organisations={organisations} busy={busy === "save"} submitLabel="Save changes" formRef={editFormRef} errorRef={editErrorRef} formError={editFormError} fieldErrors={editFieldErrors} onSubmit={save} onCancel={() => { setEditing(false); editClear(); }} />
      ) : (
        <DefinitionList
          items={[
            { term: "Who", value: BENEFICIARY_TYPE_LABELS[item.beneficiary_type] ?? item.beneficiary_type },
            { term: "Full name", value: item.full_name ?? "—" },
            { term: "Relationship", value: `${RELATIONSHIP_LABELS[item.relationship.type] ?? item.relationship.type}${item.relationship.description ? ` — ${item.relationship.description}` : ""}` },
            { term: "Authority", value: AUTHORITY_BASIS_LABELS[item.authority_basis] ?? item.authority_basis },
          ]}
        />
      )}
      {extraEvidenceText(item.beneficiary_type) && editable ? <p className="text-ink-700">{extraEvidenceText(item.beneficiary_type)}</p> : null}
      <DecisionNotice decision={v.decision} status={v.status} />
      <InformationRequests requests={v.information_requests} />
      <DocumentManager
        subjectType="BENEFICIARY"
        subjectId={item.id}
        initialDocuments={item.documents}
        requirements={item.requirements}
        editable={editable}
        heading="Evidence"
        level={3}
        anchor={`documents-${item.id}`}
        description="Documents that show who the beneficiary is and your authority to raise funds for them."
        api={api}
      />
      {editable ? (
        <Button onClick={submit} disabled={busy === "submit"}>
          {busy === "submit" ? "Submitting…" : v.status === "ADDITIONAL_INFORMATION_REQUIRED" ? "Submit again" : `Submit ${item.display_name} for verification`}
        </Button>
      ) : null}
    </Card>
  );
}

export function BeneficiariesManager({ initial, organisations, api = verificationApi }: { initial: Beneficiary[]; organisations: MyOrganisation[]; api?: VerificationApi }) {
  const [items, setItems] = useState(initial);
  const [creating, setCreating] = useState(initial.length === 0);
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<string | null>(null);
  const { formError: createFormError, fieldErrors: createFieldErrors, errorRef: createErrorRef, formRef: createFormRef, clear: createClear, fail: createFail, failWith: createFailWith } = useVerificationFeedback();
  const createdRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (created) createdRef.current?.focus();
  }, [created]);

  async function onCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const { body, errors } = buildBeneficiaryInput(new FormData(event.currentTarget));
    if (Object.keys(errors).length > 0) return createFail("Please correct the errors below.", errors);
    createClear();
    setBusy(true);
    try {
      const beneficiary = await api.createBeneficiary(body);
      setItems((current) => [beneficiary, ...current]);
      setCreating(false);
      setCreated(`${beneficiary.display_name} was added. Upload the evidence below, then submit for verification.`);
    } catch (error) {
      createFailWith(error, "We couldn't add this beneficiary. Please try again.", { "relationship.type": "relationship_type", "relationship.description": "relationship_description" });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-6">
      {created ? <FormStatus ref={createdRef} title={created} /> : null}
      {creating ? (
        <Card as="section" aria-labelledby="new-beneficiary-title">
          <h2 id="new-beneficiary-title" className="font-display text-xl font-semibold text-ink-900">
            Add a beneficiary
          </h2>
          <div className="mt-4">
            <BeneficiaryForm organisations={organisations} busy={busy} submitLabel="Add beneficiary" formRef={createFormRef} errorRef={createErrorRef} formError={createFormError} fieldErrors={createFieldErrors} onSubmit={onCreate} onCancel={items.length > 0 ? () => setCreating(false) : undefined} />
          </div>
        </Card>
      ) : (
        <Button onClick={() => { setCreated(null); setCreating(true); }}>Add a beneficiary</Button>
      )}
      <section aria-labelledby="beneficiary-list-title" className="space-y-4">
        <h2 id="beneficiary-list-title" className="font-display text-2xl font-semibold text-ink-900">
          Your beneficiaries
        </h2>
        {items.length === 0 ? (
          <EmptyState title="No beneficiaries yet" description="A beneficiary is the person, group or organisation who will benefit from the funds you raise." />
        ) : (
          <ul className="space-y-4">
            {items.map((item) => (
              <BeneficiaryCard key={item.id} initial={item} organisations={organisations} api={api} />
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
