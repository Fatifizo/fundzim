/**
 * Plain-text rendering helpers for campaign content (contract §9). Campaign text is ALWAYS plain text:
 * it is rendered through React text nodes (escaped), never with dangerouslySetInnerHTML, and never turned
 * into links. These helpers only split text into paragraphs and remove characters that have no business in
 * displayed text.
 */

// C0 controls except TAB and LF, DEL, C1 controls, bidi overrides/isolates (Trojan-Source style spoofing),
// and zero-width/invisible formatting characters.
const UNSAFE_CHARS = /[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F-\u009F​-‏‪-‮⁠-⁩﻿]/g;

/** Normalises line endings and strips control and bidi-override characters. */
export function cleanText(value: unknown): string {
  if (typeof value !== "string") return "";
  return value.replace(/\r\n?/g, "\n").replace(UNSAFE_CHARS, "");
}

/**
 * Splits a story into paragraphs on blank lines. Single newlines stay inside a paragraph (the caller renders
 * them with `white-space: pre-line`). Empty paragraphs are dropped.
 */
export function storyParagraphs(story: unknown): string[] {
  return cleanText(story)
    .split(/\n[ \t]*\n+/)
    .map((p) => p.replace(/^\n+|\n+$/g, "").trim())
    .filter((p) => p.length > 0);
}

/** Truncates on a word boundary for meta descriptions and cards (no HTML involved). */
export function truncate(value: unknown, max: number): string {
  const text = cleanText(value).replace(/\s+/g, " ").trim();
  if (text.length <= max) return text;
  const cut = text.slice(0, max - 1);
  const space = cut.lastIndexOf(" ");
  return `${(space > max * 0.6 ? cut.slice(0, space) : cut).trimEnd()}…`;
}

/** Character count as people see it (code points, not UTF-16 units), after line-ending normalisation. */
export function characterCount(value: string): number {
  return [...value.replace(/\r\n?/g, "\n")].length;
}

/** Client-side mirror of the API's content checks (contract §9) for early feedback; the API decides. */
export function contentProblem(value: string): "HTML_NOT_ALLOWED" | "UNSAFE_LINK" | null {
  if (/<\s*\/?\s*[a-zA-Z!][^>]*>/.test(value)) return "HTML_NOT_ALLOWED";
  if (/\b(?:javascript|data|vbscript)\s*:/i.test(value)) return "UNSAFE_LINK";
  return null;
}
