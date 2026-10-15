"use client";

import { useEffect, useRef, useState, type FormEvent } from "react";

import { LedgerAmount } from "@/components/admin/finance/common";
import { formatDateTime } from "@/components/account/format";
import { FormError } from "@/components/forms/form-error";
import { SelectField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { Badge } from "@/components/ui/badge";
import { feesApi, type FeesApi } from "@/lib/fees/api";
import { describeFeeError } from "@/lib/fees/errors";
import { amountToMinor, basisLabel, responsibilityLabel } from "@/lib/fees/labels";
import { readQuote } from "@/lib/fees/normalise";
import type { FeeQuote } from "@/lib/fees/types";
import { humanise } from "@/lib/payments/labels";

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** A fee amount; null is UNKNOWN (never shown as zero). */
function Money({ minor, currency }: { minor: string | null; currency: string }) {
  if (minor === null) return <Badge tone="info">UNKNOWN</Badge>;
  return <LedgerAmount minor={minor} currency={currency} />;
}

/** The fee breakdown of one quote (donor disclosure). Every figure comes from the API; nothing is computed here. */
export function QuoteBreakdown({ quote }: { quote: FeeQuote }) {
  const c = quote.currency;
  const platform = quote.components.find((x) => x.category === "PLATFORM_FEE");
  const provider = quote.components.find((x) => x.category === "PAYMENT_PROCESSING_FEE");
  return (
    <section aria-labelledby={`q-${quote.id}`} className="space-y-3 rounded-card border border-line bg-surface p-4">
      <h3 id={`q-${quote.id}`} className="font-display text-xl font-semibold text-ink-900">
        Fee breakdown <Badge tone="gold">TEST</Badge> <Badge tone="info">Estimate</Badge>
      </h3>
      <dl className="grid gap-3 sm:grid-cols-2">
        <div>
          <dt className="text-sm font-semibold text-ink-600">Donation</dt>
          <dd>
            <Money minor={quote.amount_minor} currency={c} />
          </dd>
        </div>
        <div>
          <dt className="text-sm font-semibold text-ink-600">Platform fee</dt>
          <dd>
            <Money minor={quote.platform_fee_minor} currency={c} /> · {basisLabel(quote.platform_fee_basis)}
            {platform?.responsibility ? <span className="block text-sm text-ink-700">{responsibilityLabel(platform.responsibility)}</span> : null}
          </dd>
        </div>
        <div>
          <dt className="text-sm font-semibold text-ink-600">Payment provider fee</dt>
          <dd>
            <Money minor={quote.provider_fee_minor} currency={c} /> · {basisLabel(quote.provider_fee_basis)}
            <span className="block text-sm text-ink-700">{responsibilityLabel(provider?.responsibility || null)}</span>
          </dd>
        </div>
        <div>
          <dt className="text-sm font-semibold text-ink-600">Total the donor would be charged</dt>
          <dd>
            <Money minor={quote.donor_charge_minor} currency={c} />
          </dd>
        </div>
        <div>
          <dt className="text-sm font-semibold text-ink-600">Amount for the campaign</dt>
          <dd>
            <Money minor={quote.campaign_allocation_minor} currency={c} />
          </dd>
        </div>
        <div>
          <dt className="text-sm font-semibold text-ink-600">Quote expires</dt>
          <dd>{formatDateTime(quote.expires_at)}</dd>
        </div>
      </dl>
      <p className="font-semibold text-ink-900">{quote.notice}</p>
      <p className="text-sm text-ink-700">
        Rates are TEST — synthetic configuration, not FundZim&apos;s commercial rates. Provider fees are estimates from unverified or synthetic evidence, or
        UNKNOWN. Status: {humanise(quote.status)}.
      </p>
    </section>
  );
}

const METHODS = ["ECOCASH", "ONEMONEY", "BANK_TRANSFER", "CARD"];

/**
 * Development preview of the donor fee disclosure. Signed-in only; it asks the API for a quote (a quote is not a
 * charge) and never offers to pay: public checkout stays disabled.
 */
export function DonationQuotePreview({ campaignId, api = feesApi }: { campaignId?: string; api?: FeesApi }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [quote, setQuote] = useState<FeeQuote | null>(null);
  const errorRef = useRef<HTMLDivElement>(null);
  const resultRef = useRef<HTMLDivElement>(null);
  const keyRef = useRef<{ body: string; key: string } | null>(null);
  useEffect(() => {
    if (quote) resultRef.current?.focus();
  }, [quote]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const d = new FormData(event.currentTarget);
    const campaign = String(d.get("campaign_id") ?? "").trim();
    const amount = amountToMinor(String(d.get("amount") ?? ""), "USD");
    const method = String(d.get("payment_method") ?? "");
    const errs: Record<string, string> = {};
    if (!UUID.test(campaign)) errs.campaign_id = "Paste the campaign id.";
    if (amount === null || amount === "0") errs.amount = "Enter an amount, for example 20 or 20.50.";
    setFieldErrors(errs);
    if (Object.keys(errs).length) {
      setError("Please correct the errors below.");
      requestAnimationFrame(() => errorRef.current?.focus());
      return;
    }
    const body = { campaign_id: campaign, amount_minor: amount!, currency: "USD", payment_method: method || null };
    const raw = JSON.stringify(body);
    if (!keyRef.current || keyRef.current.body !== raw) keyRef.current = { body: raw, key: crypto.randomUUID() };
    setBusy(true);
    setError(null);
    try {
      const q = readQuote(await api.createQuote(body, keyRef.current.key));
      setQuote(q);
    } catch (err) {
      setQuote(null);
      setError(describeFeeError(err).message);
      requestAnimationFrame(() => errorRef.current?.focus());
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-6">
      <form noValidate onSubmit={submit} className="space-y-5" aria-label="Preview donation fees">
        <FormError ref={errorRef} message={error} />
        <TextField label="Campaign id" name="campaign_id" autoComplete="off" defaultValue={campaignId ?? ""} error={fieldErrors.campaign_id} />
        <TextField label="Donation (USD)" name="amount" inputMode="decimal" autoComplete="off" hint="For example 20 or 20.50." error={fieldErrors.amount} />
        <SelectField label="Payment method (optional)" name="payment_method" placeholderOption="Not chosen yet" options={METHODS.map((m) => ({ value: m, label: humanise(m) }))} />
        <SubmitButton busy={busy} busyLabel="Calculating…">
          Preview fees
        </SubmitButton>
      </form>
      {quote ? (
        <div ref={resultRef} tabIndex={-1} className="outline-none">
          <QuoteBreakdown quote={quote} />
        </div>
      ) : null}
    </div>
  );
}
