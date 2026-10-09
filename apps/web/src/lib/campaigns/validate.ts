/**
 * Client-side checks for campaign content (contract §9 policy values). Early feedback only: the API applies
 * the authoritative policy and its error codes are mapped onto the same fields.
 */
import { AMOUNT_PROBLEM_MESSAGES, parseDecimalToMinor } from "./money";
import { characterCount, contentProblem } from "./text";
import type { CurrencyInfo } from "./types";

export const LIMITS = {
  title: { min: 10, max: 120 },
  summary: { min: 20, max: 300 },
  story: { min: 100, max: 20_000 },
  updateTitle: { min: 3, max: 120 },
  updateBody: { min: 10, max: 5_000 },
} as const;

export interface DraftFields {
  category: string;
  title: string;
  summary: string;
  story: string;
  currency: string;
  amount: string;
}

function lengthError(label: string, value: string, min: number, max: number): string | null {
  const n = characterCount(value.trim());
  if (n === 0) return `Enter a ${label.toLowerCase()}.`;
  if (n < min) return `${label} must be at least ${min} characters (now ${n}).`;
  if (n > max) return `${label} must be at most ${max.toLocaleString("en")} characters (now ${n.toLocaleString("en")}).`;
  return null;
}

function contentError(value: string): string | null {
  const p = contentProblem(value);
  if (p === "HTML_NOT_ALLOWED") return "Use plain text only: HTML tags such as <b> or <script> are not allowed.";
  if (p === "UNSAFE_LINK") return "Remove links that start with javascript:, data: or vbscript:.";
  return null;
}

export type DraftSection = "category" | "basics" | "story" | "goal";

/** Field errors for one section of the draft (keys are form field names). */
export function validateSection(section: DraftSection, f: DraftFields, currencies: readonly CurrencyInfo[]): Record<string, string> {
  const errors: Record<string, string> = {};
  const set = (k: string, v: string | null) => {
    if (v) errors[k] = v;
  };
  if (section === "category") {
    if (!f.category) errors.category = "Choose a category.";
  }
  if (section === "basics") {
    set("title", lengthError("Title", f.title, LIMITS.title.min, LIMITS.title.max) ?? contentError(f.title));
    set("summary", lengthError("Summary", f.summary, LIMITS.summary.min, LIMITS.summary.max) ?? contentError(f.summary));
  }
  if (section === "story") {
    set("story", lengthError("Story", f.story, LIMITS.story.min, LIMITS.story.max) ?? contentError(f.story));
  }
  if (section === "goal") {
    const currency = currencies.find((c) => c.code === f.currency);
    if (!currency) errors.currency = "Choose a currency.";
    else {
      const parsed = parseDecimalToMinor(f.amount, currency.minor_units);
      if (!parsed.ok) errors.amount = AMOUNT_PROBLEM_MESSAGES[parsed.problem];
    }
  }
  return errors;
}

/** Goal for the API, or null when the amount or currency is not valid. */
export function goalFromFields(f: Pick<DraftFields, "currency" | "amount">, currencies: readonly CurrencyInfo[]): { amount_minor: string; currency: string } | null {
  const currency = currencies.find((c) => c.code === f.currency);
  if (!currency) return null;
  const parsed = parseDecimalToMinor(f.amount, currency.minor_units);
  return parsed.ok ? { amount_minor: parsed.amountMinor, currency: currency.code } : null;
}
