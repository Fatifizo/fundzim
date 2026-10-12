import { first, isCategoryFilter, isCursor } from "./paths";
import { cleanText } from "./text";

export interface ListQuery {
  q: string;
  /** Category code or slug (validated). */
  category: string;
  sort: "published" | "created";
  cursor: string;
}

/**
 * Listing search parameters, validated before they reach the API: `q` ≤ 100 characters without control
 * characters (the API refuses them with 422), category code or slug, known sort, well-formed cursor.
 */
export function readListQuery(p: Record<string, string | string[] | undefined>): ListQuery {
  const q = cleanText(first(p.q) ?? "")
    .replace(/[\n\t]/g, " ")
    .trim();
  const categoryRaw = first(p.category);
  const cursorRaw = first(p.cursor);
  return {
    q: [...q].slice(0, 100).join("").trim(),
    category: isCategoryFilter(categoryRaw) ? categoryRaw : "",
    sort: first(p.sort) === "created" ? "created" : "published",
    cursor: isCursor(cursorRaw) ? cursorRaw : "",
  };
}

/** "1 campaign", "24 campaigns", "1000+ campaigns" (a capped total is a lower bound). */
export function resultCountText(total: { value: number; capped: boolean }): string {
  if (total.capped) return `${total.value}+ campaigns`;
  return total.value === 1 ? "1 campaign" : `${total.value.toLocaleString("en-GB")} campaigns`;
}
