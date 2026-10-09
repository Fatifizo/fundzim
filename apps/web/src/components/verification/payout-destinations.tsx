"use client";

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
import { EmptyState } from "@/components/ui/empty-state";
import { verificationApi, type VerificationApi } from "@/lib/verification/api";
import {
  complianceCheckLabel,
  CURRENCY_LABELS,
  destinationStatusLabel,
  documentTypeLabel,
  METHOD_LABELS,
  ownershipLabel,
  RAIL_LABELS,
  sideLabel,
} from "@/lib/verification/labels";
import { BANK_RAILS, type Beneficiary, type MyOrganisation, type PayoutDestination, type PayoutDestinationInput, type Rail, type VerificationDocument, type VerificationMethod } from "@/lib/verification/types";

import { DocumentManager } from "./document-manager";
import { PayoutsUnavailableNotice } from "./status";
import { useVerificationFeedback } from "./use-verification-feedback";

/** Verification can be (re)requested from these states (ADR-034 §4) or when ownership still lacks evidence. */
export function canRequestVerification(d: PayoutDestination): boolean {
  if (d.status === "RETIRED") return false;
  if (["UNVERIFIED", "REJECTED", "EXPIRED", "SUSPENDED"].includes(d.status)) return true;
  return d.status === "PENDING_VERIFICATION" && (d.checks.ownership === "PROVIDER_CONFIRMATION_REQUIRED" || d.checks.ownership === "FAILED" || d.checks.ownership === "NOT_STARTED");
}

export function methodsFor(rail: string): VerificationMethod[] {
  return BANK_RAILS.has(rail) ? ["BANK_LETTER", "PROVIDER_LOOKUP"] : ["MOBILE_MONEY_STATEMENT", "PROVIDER_LOOKUP"];
}

/** Normalises what the user typed for the account (spaces and dashes removed). Format rules live in the API. */
export function normaliseAccountIdentifier(raw: string): string {
  return raw.replace(/[\s-]+/g, "");
}

export function buildDestinationInput(data: FormData): { body: PayoutDestinationInput; errors: Record<string, string> } {
  const text = (key: string) => String(data.get(key) ?? "").trim();
  const errors: Record<string, string> = {};
  const rail = text("rail") as Rail;
  const currency = text("currency") as "USD" | "ZWG";
  const payeeType = (text("payee_type") || "OWNER") as "OWNER" | "BENEFICIARY";
  const beneficiaryId = text("beneficiary_id");
  const holder = text("holder_name").replace(/\s+/g, " ");
  const account = normaliseAccountIdentifier(text("account_identifier"));
  const bankCode = text("bank_code");
  if (!rail) errors.rail = "Choose how the money would be received.";
  if (currency !== "USD" && currency !== "ZWG") errors.currency = "Choose a currency.";
  if (payeeType === "BENEFICIARY" && !beneficiaryId) errors.beneficiary_id = "Choose the beneficiary who owns this account.";
  if (holder.length < 2) errors.holder_name = "Enter the account holder's name exactly as the provider has it.";
  if (!account) errors.account_identifier = BANK_RAILS.has(rail) ? "Enter the account number." : "Enter the mobile money number.";
  else if (!/^\+?[0-9A-Za-z]{4,34}$/.test(account)) errors.account_identifier = "Use digits only (and letters for some bank accounts).";
  if (BANK_RAILS.has(rail) && !bankCode) errors.bank_code = "Enter the bank or branch code.";
  const body: PayoutDestinationInput = {
    payee: payeeType === "BENEFICIARY" ? { type: "BENEFICIARY", beneficiary_id: beneficiaryId } : { type: "OWNER" },
    rail,
    currency,
    holder_name: holder,
    account_identifier: account,
  };
  const owner = text("owner_organisation_id");
  if (owner) body.owner_organisation_id = owner;
  if (bankCode) body.bank_code = bankCode;
  return { body, errors };
}

function Checks({ destination }: { destination: PayoutDestination }) {
  const ownership = ownershipLabel(destination.checks.ownership);
  const compliance = complianceCheckLabel(destination.checks.compliance);
  return (
    <div>
      <h4 className="font-semibold text-ink-900">Checks (each is separate)</h4>
      <dl className="mt-2 grid gap-3 sm:grid-cols-3">
        <div className="rounded-xl border border-line p-3">
          <dt className="text-sm font-semibold text-ink-600">Format</dt>
          <dd className="mt-1">
            {destination.checks.format_validated ? <Badge tone="brand">Valid format</Badge> : <Badge tone="neutral">Not checked</Badge>}
            <p className="mt-1 text-sm text-ink-700">Only checks that the number looks right — not who owns it.</p>
          </dd>
        </div>
        <div className="rounded-xl border border-line p-3">
          <dt className="text-sm font-semibold text-ink-600">Ownership</dt>
          <dd className="mt-1">
            <Badge tone={ownership.tone}>{ownership.label}</Badge>
            <p className="mt-1 text-sm text-ink-700">{ownership.description}</p>
          </dd>
        </div>
        <div className="rounded-xl border border-line p-3">
          <dt className="text-sm font-semibold text-ink-600">Compliance review</dt>
          <dd className="mt-1">
            <Badge tone={compliance.tone}>{compliance.label}</Badge>
          </dd>
        </div>
      </dl>
    </div>
  );
}

function DestinationCard({ initial, beneficiaries, api, onRetired }: { initial: PayoutDestination; beneficiaries: Beneficiary[]; api: VerificationApi; onRetired: (id: string) => void }) {
  const [item, setItem] = useState(initial);
  const [mode, setMode] = useState<null | "edit" | "verify">(null);
  const [busy, setBusy] = useState<null | "save" | "verify" | "retire">(null);
  const [status, setStatus] = useState<string | null>(null);
  const [confirmRetire, setConfirmRetire] = useState(false);
  const [documents, setDocuments] = useState<VerificationDocument[]>([]);
  const [method, setMethod] = useState<VerificationMethod>(methodsFor(initial.rail)[0]!);
  const { formError: editFormError, fieldErrors: editFieldErrors, errorRef: editErrorRef, formRef: editFormRef, clear: editClear, fail: editFail, failWith: editFailWith } = useVerificationFeedback();
  const { formError: verifyFormError, fieldErrors: verifyFieldErrors, errorRef: verifyErrorRef, formRef: verifyFormRef, clear: verifyClear, fail: verifyFail, failWith: verifyFailWith } = useVerificationFeedback();
  const statusRef = useRef<HTMLDivElement>(null);
  const titleId = `destination-${item.id}`;
  const statusLabel = destinationStatusLabel(item.status);
  const bank = BANK_RAILS.has(item.rail);

  useEffect(() => {
    if (status) statusRef.current?.focus();
  }, [status]);

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const holder = String(data.get("holder_name") ?? "").trim().replace(/\s+/g, " ");
    const account = normaliseAccountIdentifier(String(data.get("account_identifier") ?? "").trim());
    const bankCode = String(data.get("bank_code") ?? "").trim();
    const errors: Record<string, string> = {};
    if (holder.length < 2) errors.holder_name = "Enter the account holder's name.";
    if (account && !/^\+?[0-9A-Za-z]{4,34}$/.test(account)) errors.account_identifier = "Use digits only (and letters for some bank accounts).";
    if (Object.keys(errors).length > 0) return editFail("Please correct the errors below.", errors);
    editClear();
    setBusy("save");
    try {
      const body: { holder_name?: string; account_identifier?: string; bank_code?: string } = { holder_name: holder };
      if (account) body.account_identifier = account;
      if (bank && bankCode) body.bank_code = bankCode;
      setItem(await api.updateDestination(item.id, body, item.version));
      setMode(null);
      setStatus("Changes saved. Because the details changed, this account needs to be verified again.");
    } catch (error) {
      editFailWith(error, "We couldn't save the changes. Please try again.");
    } finally {
      setBusy(null);
    }
  }

  async function requestVerification(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const ids = data.getAll("document_ids").map(String);
    if (method !== "PROVIDER_LOOKUP" && ids.length === 0) {
      return verifyFail("Please correct the errors below.", { document_ids: "Upload your evidence above and select it here." });
    }
    verifyClear();
    setBusy("verify");
    try {
      const updated = await api.requestDestinationVerification(item.id, method, method === "PROVIDER_LOOKUP" ? [] : ids);
      setItem(updated);
      setMode(null);
      setStatus(
        updated.checks.ownership === "PROVIDER_CONFIRMATION_REQUIRED"
          ? "Verification requested. An automatic check with this provider is not available yet, so ownership needs provider confirmation or other evidence."
          : "Verification requested. We will let you know when it has been reviewed.",
      );
    } catch (error) {
      verifyFailWith(error, "We couldn't request verification. Please try again.");
    } finally {
      setBusy(null);
    }
  }

  async function retire() {
    setConfirmRetire(false);
    setBusy("retire");
    try {
      await api.retireDestination(item.id);
      onRetired(item.id);
    } catch (error) {
      editFailWith(error, "We couldn't remove this account. Please try again.");
      setBusy(null);
    }
  }

  const cleanDocs = documents.filter((d) => d.status === "CLEAN");

  return (
    <Card as="li" aria-labelledby={titleId} className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 id={titleId} className="font-display text-xl font-semibold break-words text-ink-900">
            {RAIL_LABELS[item.rail] ?? item.rail} · {item.masked_identifier}
          </h3>
          <p className="mt-1 flex flex-wrap items-center gap-2 text-ink-700">
            <Badge tone={statusLabel.tone}>{statusLabel.label}</Badge>
            <span>{CURRENCY_LABELS[item.currency] ?? item.currency}</span>
            <span>Holder: {item.holder_name}</span>
          </p>
          {item.payee.type === "BENEFICIARY" ? (
            <p className="mt-1 text-sm text-ink-600">Belongs to beneficiary: {beneficiaries.find((b) => b.id === item.payee.beneficiary_id)?.display_name ?? "a beneficiary"}</p>
          ) : null}
        </div>
      </div>
      {status ? <FormStatus ref={statusRef} title={status} /> : null}
      <Checks destination={item} />
      <p className="text-ink-700">
        <span className="font-semibold">Can receive payouts:</span> No — payouts are not available yet.
        {item.last_reviewed_at ? <span className="block text-sm text-ink-600">Last reviewed {formatDateTime(item.last_reviewed_at)}</span> : null}
      </p>
      <FormError ref={editErrorRef} message={mode === "edit" ? null : editFormError} />

      {mode === "edit" ? (
        <form ref={editFormRef} noValidate onSubmit={save} className="space-y-4 rounded-xl border border-line bg-surface-muted p-4" aria-label={`Edit ${RAIL_LABELS[item.rail]} account`}>
          <FormError ref={editErrorRef} message={editFormError} />
          <Alert tone="notice" title="Changing details resets verification">
            After a change the account must be verified again before it could ever be used.
          </Alert>
          <TextField label="Account holder name" name="holder_name" defaultValue={item.holder_name} error={editFieldErrors.holder_name} maxLength={150} autoComplete="off" />
          <TextField
            label={bank ? "New account number (optional)" : "New mobile money number (optional)"}
            name="account_identifier"
            hint={`Currently ${item.masked_identifier}. Leave empty to keep it.`}
            inputMode={bank ? "text" : "tel"}
            autoComplete="off"
            spellCheck={false}
            error={editFieldErrors.account_identifier}
            maxLength={40}
          />
          {bank ? <TextField label="Bank or branch code (optional)" name="bank_code" autoComplete="off" maxLength={20} /> : null}
          <div className="flex flex-wrap gap-3">
            <SubmitButton busy={busy === "save"} busyLabel="Saving…">
              Save changes
            </SubmitButton>
            <Button variant="outline" onClick={() => { setMode(null); editClear(); }}>
              Cancel
            </Button>
          </div>
        </form>
      ) : null}

      {mode === "verify" ? (
        <div className="space-y-4 rounded-xl border border-line bg-surface-muted p-4">
          <SelectField
            label="How can you show this account is yours?"
            name="method"
            value={method}
            onChange={(event) => setMethod(event.target.value as VerificationMethod)}
            options={methodsFor(item.rail).map((m) => ({ value: m, label: METHOD_LABELS[m] ?? m }))}
            hint={method === "PROVIDER_LOOKUP" ? "Automatic checks with providers are not available yet. Choosing this records your request, but ownership will show as needing provider confirmation." : undefined}
          />
          {method !== "PROVIDER_LOOKUP" ? (
            <DocumentManager
              subjectType="PAYOUT_DESTINATION"
              subjectId={item.id}
              initialDocuments={[]}
              editable
              heading="Ownership evidence"
              level={3}
              anchor={`documents-${item.id}`}
              description={bank ? "A letter or statement from your bank showing the account holder name and account number." : "A recent statement from your mobile money provider showing your name and number."}
              onDocumentsChange={setDocuments}
              api={api}
            />
          ) : null}
          <form ref={verifyFormRef} noValidate onSubmit={requestVerification} className="space-y-4" aria-label="Request verification">
            <FormError ref={verifyErrorRef} message={verifyFormError} />
            {method !== "PROVIDER_LOOKUP" ? (
              <fieldset aria-describedby={verifyFieldErrors.document_ids ? `${titleId}-docs-error` : undefined} aria-invalid={verifyFieldErrors.document_ids ? true : undefined} tabIndex={verifyFieldErrors.document_ids ? -1 : undefined}>
                <legend className="font-semibold text-ink-900">Evidence to send</legend>
                {cleanDocs.length === 0 ? <p className="text-sm text-ink-700">Uploaded files appear here once they have passed the security check.</p> : null}
                {cleanDocs.map((doc) => (
                  <label key={doc.id} className="flex min-h-11 items-center gap-3 text-ink-900">
                    <input type="checkbox" name="document_ids" value={doc.id} defaultChecked className="size-5 accent-brand-700" />
                    {documentTypeLabel(doc.document_type)}
                    {sideLabel(doc.side) ? ` (${sideLabel(doc.side).toLowerCase()})` : ""}
                  </label>
                ))}
                {verifyFieldErrors.document_ids ? (
                  <p id={`${titleId}-docs-error`} className="text-sm font-semibold text-danger-700">
                    <span aria-hidden="true">Error: </span>
                    {verifyFieldErrors.document_ids}
                  </p>
                ) : null}
              </fieldset>
            ) : null}
            <div className="flex flex-wrap gap-3">
              <SubmitButton busy={busy === "verify"} busyLabel="Sending…">
                Request verification
              </SubmitButton>
              <Button variant="outline" onClick={() => { setMode(null); verifyClear(); }}>
                Cancel
              </Button>
            </div>
          </form>
        </div>
      ) : null}

      {mode === null && item.status !== "RETIRED" ? (
        <div className="flex flex-wrap gap-3">
          {canRequestVerification(item) ? <Button onClick={() => setMode("verify")}>Verify this account</Button> : null}
          <Button variant="outline" onClick={() => setMode("edit")}>
            Change details
          </Button>
          <Button variant="ghost" onClick={() => setConfirmRetire(true)} disabled={busy === "retire"}>
            Remove
          </Button>
        </div>
      ) : null}

      {confirmRetire ? (
        <Dialog title="Remove this account?" description={`${RAIL_LABELS[item.rail]} ${item.masked_identifier} will no longer be offered as a payout account. Its history is kept.`} onClose={() => setConfirmRetire(false)}>
          <div className="flex flex-wrap gap-3">
            <Button onClick={retire}>Remove account</Button>
            <Button variant="outline" onClick={() => setConfirmRetire(false)}>
              Keep it
            </Button>
          </div>
        </Dialog>
      ) : null}
    </Card>
  );
}

export function PayoutDestinationsManager({
  initial,
  beneficiaries,
  organisations,
  api = verificationApi,
}: {
  initial: PayoutDestination[];
  beneficiaries: Beneficiary[];
  organisations: MyOrganisation[];
  api?: VerificationApi;
}) {
  const [items, setItems] = useState(initial.filter((d) => d.status !== "RETIRED"));
  const [creating, setCreating] = useState(false);
  const [busy, setBusy] = useState(false);
  const [rail, setRail] = useState<string>("");
  const [payeeType, setPayeeType] = useState<"OWNER" | "BENEFICIARY">("OWNER");
  const [notice, setNotice] = useState<string | null>(null);
  const { formError: createFormError, fieldErrors: createFieldErrors, errorRef: createErrorRef, formRef: createFormRef, clear: createClear, fail: createFail, failWith: createFailWith } = useVerificationFeedback();
  const noticeRef = useRef<HTMLDivElement>(null);
  const adminOrgs = organisations.filter((o) => o.my_role === "ORG_ADMIN");
  const bank = BANK_RAILS.has(rail);

  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);

  async function onCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const { body, errors } = buildDestinationInput(new FormData(form));
    if (Object.keys(errors).length > 0) return createFail("Please correct the errors below.", errors);
    createClear();
    setBusy(true);
    try {
      const destination = await api.createDestination(body);
      form.reset(); // drops the typed account number from the page
      setItems((current) => [destination, ...current]);
      setCreating(false);
      setNotice(`${RAIL_LABELS[destination.rail] ?? destination.rail} account ${destination.masked_identifier} was added. Verify it to show it is yours.`);
    } catch (error) {
      createFailWith(error, "We couldn't add this account. Please try again.", { "payee.beneficiary_id": "beneficiary_id" });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-6">
      <PayoutsUnavailableNotice />
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      {creating ? (
        <Card as="section" aria-labelledby="new-destination-title">
          <h2 id="new-destination-title" className="font-display text-xl font-semibold text-ink-900">
            Add a payout account
          </h2>
          <form ref={createFormRef} noValidate onSubmit={onCreate} className="mt-4 space-y-5">
            <FormError ref={createErrorRef} message={createFormError} />
            {adminOrgs.length > 0 ? (
              <SelectField label="Whose account list is this for?" name="owner_organisation_id" options={[{ value: "", label: "Mine, personally" }, ...adminOrgs.map((o) => ({ value: o.id, label: o.display_name }))]} defaultValue="" />
            ) : null}
            <SelectField
              label="Whose account is it?"
              name="payee_type"
              value={payeeType}
              onChange={(event) => setPayeeType(event.target.value as "OWNER" | "BENEFICIARY")}
              options={[
                { value: "OWNER", label: "Mine (or my organisation's)" },
                ...(beneficiaries.length > 0 ? [{ value: "BENEFICIARY", label: "A beneficiary's" }] : []),
              ]}
            />
            {payeeType === "BENEFICIARY" ? (
              <SelectField label="Beneficiary" name="beneficiary_id" placeholderOption="Choose a beneficiary" options={beneficiaries.map((b) => ({ value: b.id, label: b.display_name }))} error={createFieldErrors.beneficiary_id} />
            ) : null}
            <SelectField label="How would money be received?" name="rail" placeholderOption="Choose one" options={toOptions(RAIL_LABELS)} value={rail} onChange={(event) => setRail(event.target.value)} error={createFieldErrors.rail} />
            <SelectField label="Currency" name="currency" placeholderOption="Choose a currency" options={toOptions(CURRENCY_LABELS)} error={createFieldErrors.currency} hint="An account receives one currency. FundZim never converts currencies." />
            <TextField label="Account holder name" name="holder_name" hint="Exactly as registered with the bank or mobile money provider." autoComplete="off" error={createFieldErrors.holder_name} maxLength={150} />
            <TextField
              label={bank ? "Account number" : "Mobile money number"}
              name="account_identifier"
              hint={bank ? "We only ever show the last few digits after you save." : "For example 0771234567. We only ever show the last few digits after you save."}
              inputMode={bank ? "text" : "tel"}
              autoComplete="off"
              spellCheck={false}
              error={createFieldErrors.account_identifier}
              maxLength={40}
            />
            {bank ? <TextField label="Bank or branch code" name="bank_code" autoComplete="off" error={createFieldErrors.bank_code} maxLength={20} /> : null}
            <div className="flex flex-wrap gap-3">
              <SubmitButton busy={busy} busyLabel="Adding…">
                Add account
              </SubmitButton>
              <Button variant="outline" onClick={() => { setCreating(false); createClear(); }}>
                Cancel
              </Button>
            </div>
          </form>
        </Card>
      ) : (
        <Button onClick={() => { setNotice(null); setCreating(true); }}>Add a payout account</Button>
      )}
      <section aria-labelledby="destination-list-title" className="space-y-4">
        <h2 id="destination-list-title" className="font-display text-2xl font-semibold text-ink-900">
          Your payout accounts
        </h2>
        {items.length === 0 ? (
          <EmptyState title="No payout accounts yet" description="Add the bank account or mobile money wallet where funds would be sent once payouts exist." />
        ) : (
          <ul className="space-y-4">
            {items.map((item) => (
              <DestinationCard key={item.id} initial={item} beneficiaries={beneficiaries} api={api} onRetired={(id) => { setItems((current) => current.filter((d) => d.id !== id)); setNotice("The account was removed."); }} />
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
