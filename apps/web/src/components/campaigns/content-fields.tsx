"use client";

import { useId } from "react";

import { SelectField, TextAreaField } from "@/components/forms/select-field";
import { TextField } from "@/components/forms/text-field";
import { characterCount } from "@/lib/campaigns/text";
import type { Category, CurrencyInfo } from "@/lib/campaigns/types";
import { LIMITS, type DraftFields } from "@/lib/campaigns/validate";

/**
 * Controlled form fields for campaign content, shared by the wizard and the edit page. Every field has a
 * visible label, instructions before the input, and its error linked with aria-describedby (FRONTEND §6).
 * Field ids are `field-<name>` so error summaries can link to them.
 */
type Change = (patch: Partial<DraftFields>) => void;

export function CategoryField({ categories, value, onChange, error, hasOrganisation }: { categories: Category[]; value: string; onChange: Change; error?: string; hasOrganisation: boolean }) {
  const hintId = useId();
  return (
    <fieldset id="field-category" tabIndex={-1} aria-describedby={[error ? `${hintId}-error` : null, hintId].filter(Boolean).join(" ")} aria-invalid={error ? true : undefined} className="space-y-3 focus:outline-none">
      <legend className="font-semibold text-ink-900">Category</legend>
      <p id={hintId} className="text-sm text-ink-600">
        Choose what the funds are for. Reviewers check that the campaign fits its category.
      </p>
      {categories.length === 0 ? <p className="text-ink-700">No categories are available right now. Please try again later.</p> : null}
      <div className="grid gap-2 sm:grid-cols-2">
        {categories.map((c) => (
          <label key={c.code} className="flex min-h-11 cursor-pointer items-start gap-3 rounded-xl border-2 border-line p-3 text-ink-900 has-[:checked]:border-brand-700 has-[:checked]:bg-brand-50">
            <input type="radio" name="category" value={c.code} checked={value === c.code} onChange={() => onChange({ category: c.code })} className="mt-1 size-5 shrink-0 accent-brand-700" />
            <span>
              <span className="block font-semibold">{c.name}</span>
              {c.description ? <span className="block text-sm text-ink-600">{c.description}</span> : null}
              {c.requires_organisation ? <span className="block text-sm text-ink-700">{hasOrganisation ? "Organisations only." : "Organisations only: needs a verified organisation."}</span> : null}
            </span>
          </label>
        ))}
      </div>
      {error ? (
        <p id={`${hintId}-error`} className="text-sm font-semibold text-danger-700">
          <span aria-hidden="true">Error: </span>
          {error}
        </p>
      ) : null}
    </fieldset>
  );
}

function Count({ value, min, max }: { value: string; min: number; max: number }) {
  const n = characterCount(value);
  return (
    <>
      {n.toLocaleString("en")} of {max.toLocaleString("en")} characters{n < min ? ` (at least ${min})` : ""}.
    </>
  );
}

export function BasicsFields({ value, onChange, errors }: { value: Pick<DraftFields, "title" | "summary">; onChange: Change; errors: Record<string, string> }) {
  return (
    <div className="space-y-5">
      <TextField
        id="field-title"
        label="Title"
        name="title"
        value={value.title}
        onChange={(e) => onChange({ title: e.target.value })}
        maxLength={LIMITS.title.max + 20}
        autoComplete="off"
        hint={
          <>
            A short, clear name for your campaign. <Count value={value.title} min={LIMITS.title.min} max={LIMITS.title.max} />
          </>
        }
        error={errors.title}
      />
      <TextAreaField
        id="field-summary"
        label="Summary"
        name="summary"
        rows={3}
        value={value.summary}
        onChange={(e) => onChange({ summary: e.target.value })}
        hint={
          <>
            One or two sentences shown on cards and link previews. <Count value={value.summary} min={LIMITS.summary.min} max={LIMITS.summary.max} />
          </>
        }
        error={errors.summary}
      />
    </div>
  );
}

export function StoryField({ value, onChange, error }: { value: string; onChange: Change; error?: string }) {
  return (
    <TextAreaField
      id="field-story"
      label="Story"
      name="story"
      rows={12}
      value={value}
      onChange={(e) => onChange({ story: e.target.value })}
      hint={
        <>
          Explain who the funds are for, what they will pay for and why. Plain text only: leave a blank line between paragraphs. Links are shown as text, not made clickable.{" "}
          <Count value={value} min={LIMITS.story.min} max={LIMITS.story.max} />
        </>
      }
      error={error}
    />
  );
}

export function GoalFields({ value, onChange, errors, currencies }: { value: Pick<DraftFields, "currency" | "amount">; onChange: Change; errors: Record<string, string>; currencies: CurrencyInfo[] }) {
  const currency = currencies.find((c) => c.code === value.currency);
  return (
    <div className="space-y-5">
      <SelectField
        id="field-currency"
        label="Currency"
        name="currency"
        value={value.currency}
        onChange={(e) => onChange({ currency: e.target.value })}
        placeholderOption={currencies.length === 0 ? "No currency available" : "Choose a currency"}
        options={currencies.map((c) => ({ value: c.code, label: c.code === "ZWG" ? "ZiG (ZWG)" : `${c.code} (${c.display_symbol})` }))}
        hint="Only currencies FundZim can currently accept for campaigns are listed. Funds are never converted between currencies."
        error={errors.currency}
      />
      <TextField
        id="field-amount"
        label={`Goal amount${currency ? ` in ${currency.code === "ZWG" ? "ZiG" : currency.code}` : ""}`}
        name="amount"
        inputMode="decimal"
        autoComplete="off"
        value={value.amount}
        onChange={(e) => onChange({ amount: e.target.value })}
        hint={`For example 1500 or 1,500.${"0".repeat(Math.max(1, currency?.minor_units ?? 2))}. Set what you need; reviewers may ask how you arrived at it.`}
        error={errors.amount}
      />
    </div>
  );
}
