/** "1 campaign", "24 campaigns", "1000+ campaigns" (a capped total is a lower bound). */
export function resultCountText(total: { value: number; capped: boolean }): string {
  if (total.capped) return `${total.value}+ campaigns`;
  return total.value === 1 ? "1 campaign" : `${total.value.toLocaleString("en-GB")} campaigns`;
}
